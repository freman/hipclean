package main

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh/terminal"

	"github.com/howeyc/gopass"

	"github.com/PuerkitoBio/goquery"
	"github.com/matryer/try"
)

const retryInterval = 60
const retryLimit = 6

type person struct {
	Name   string
	ID     string
	Joined time.Time
}

func getCredentials() (string, string) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter Username: ")
	username, _ := reader.ReadString('\n')

	fmt.Print("Enter Password: ")
	password := gopass.GetPasswd()
	fmt.Println("")

	return strings.TrimSpace(username), strings.TrimSpace(string(password))
}

func promptPeople(limit int) []interface{} {
	fmt.Println("Enter one or more users to delete, if you know the ID of a user that doesn't appear on the above list you can enter it by prefixing a hash(#)")
	fmt.Print("Select users [eg: 1,3,5..12,#12345672]: ")
	reader := bufio.NewReader(os.Stdin)
	stringList, _ := reader.ReadString('\n')

	list := make([]interface{}, 0)
	for _, entry := range strings.Split(stringList, ",") {
		if strings.HasPrefix(entry, "#") {
			list = append(list, strings.TrimPrefix(entry, "#"))
		} else if strings.Contains(entry, "..") {
			indexRange := strings.Split(entry, "..")

			if len(indexRange) != 2 {
				log.Fatal("Wrong number of arguments in range")
			}

			min, err := strconv.Atoi(strings.TrimSpace(indexRange[0]))

			if err != nil {
				log.Fatal(err)
			}

			max, err := strconv.Atoi(strings.TrimSpace(indexRange[1]))

			for i := min; i <= max; i++ {
				if i > 0 && i <= limit {
					list = append(list, i-1)
				}
			}
		} else {
			val, err := strconv.Atoi(strings.TrimSpace(entry))
			if err != nil {
				log.Fatal(err)
			}

			if val > 0 && val <= limit {
				list = append(list, val-1)
			}
		}
	}

	return list
}

func extractProfileMetadata(gq *goquery.Document) (string, time.Time, error) {
	uid, ok := gq.Find("meta[name=uid]").Attr("content")
	if !ok {
		log.Println("Can't find uid, there might be something wrong")
	}

	var err error
	var created time.Time
	screated, ok := gq.Find("meta[name=ucreated]").Attr("content")
	if !ok {
		return "", nullTime, fmt.Errorf("Error reading account creation date")
	} else {
		created, err = time.Parse("2006-01-02 15:04:05", screated)
		if err != nil {
			return "", nullTime, err
		}
	}

	return uid, created, nil
}

func main() {
	user, pass := getCredentials()

	cookieJar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: cookieJar}

	log.Println("Grabbing XSRF Token")
	resp, err := client.Get("https://www.hipchat.com/sign_in")
	if err != nil {
		log.Fatal(err)
	}
	gq := mustParseResponse(resp)

	xsrf, exists := gq.Find("input[name=xsrf_token]").Attr("value")
	if !exists {
		log.Fatal("Can't find xsrf_token")
	}

	log.Println("Logging in")
	resp, err = client.PostForm("https://www.hipchat.com/sign_in", url.Values{
		"xsrf_token":     {xsrf},
		"email":          {user},
		"password":       {pass},
		"stay_signed_in": {"1"},
		"signin":         {"log in"},
	})
	if err != nil {
		log.Fatal(err)
	}

	gq = mustParseResponse(resp)
	if gq.Find("div.aui-page-header-main h1").Size() != 1 {
		log.Fatal("Couldn't find welcome header")
	}

	uid, created, err := extractProfileMetadata(gq)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Found user %s created %s", uid, created)

	domain := resp.Request.URL.Host

	log.Println("Listing people")

	// Get first page
	resp, err = client.Get("https://" + domain + "/people")
	if err != nil {
		log.Fatal(err)
	}
	gq = mustParseResponse(resp)

	people := make([]person, 0)

	// Figure out how many pages there are (the next/prev are links in aui-nav, but page 1 is not a link so just subtract one)
	pages := gq.Find("ol.aui-nav a").Size() - 1

	longestName := 0
	for page := 1; page <= pages; page++ {
		gq.Find("a.name").Each(func(i int, s *goquery.Selection) {
			attr, exists := s.Attr("href")
			name := strings.TrimSpace(s.Text())
			signup := parseSignupDate(strings.TrimSpace(s.Parent().Parent().SiblingsFiltered("[headers=date-joined]").Text()))

			if !exists {
				log.Println("Can't find the href for this item")
				return
			}

			longestName = maxInt(longestName, len(name))

			people = append(people, person{
				Name:   name,
				ID:     strings.TrimPrefix(attr, "/people/show/"),
				Joined: signup,
			})
		})

		if page < pages {
			log.Printf("Getting people page %d", page+1)
			resp, err = client.Get("https://" + domain + "/people?p=" + strconv.Itoa(page+1))
			if err != nil {
				log.Fatal(err)
			}
			gq = mustParseResponse(resp)
		}
	}

	// Doesn't seem to on windows
	width, _, _ := terminal.GetSize(0)
	peopleDigits := len(strconv.Itoa(len(people)))

	columns := width / (longestName + peopleDigits + 6)
	if columns < 1 {
		columns = 1
	}

	peopleDigitsString := strconv.Itoa(peopleDigits)
	for i, person := range people {
		index := i + 1 // offset by 1 for input
		padding := strings.Repeat(".", longestName-len(person.Name))
		fmt.Printf("%"+peopleDigitsString+"d:%s%s    ", index, padding, person.Name)
		if index%columns == 0 {
			fmt.Println("")
		}
	}

	// extra newline for when the columns don't line up
	if len(people)%columns != 0 {
		fmt.Println("")
	}

	historiesToDelete := promptPeople(len(people))

	for _, index := range historiesToDelete {
		var currentPerson person
		switch t := index.(type) {
		case int:
			currentPerson = people[t]
		case string:
			log.Printf("Resolving %s to a user", t)
			err = func() error {
				resp, err = client.Get("https://" + domain + "/people/show/" + t)
				if err != nil {
					return fmt.Errorf("Can't lookup %s: %s", t, err)
				}

				gq, err := goquery.NewDocumentFromResponse(resp)
				if err != nil {
					return fmt.Errorf("Can't parse %s: %s", t, err)
				}

				name := gq.Find("div.aui-item h2").Text()
				if name == "" {
					return fmt.Errorf("Can't find name for %s", t)
				}

				_, joined, err := extractProfileMetadata(gq)
				if err != nil {
					return err
				}

				currentPerson = person{
					Name:   name,
					Joined: joined,
					ID:     t,
				}
				return nil
			}()
			if err != nil {
				log.Printf("ERR: %s", err)
				continue
			}
		}
		endDate := created.Add(-1 * 24 * time.Hour)
		if endDate.Before(currentPerson.Joined) {
			endDate = currentPerson.Joined
		}

		for working := time.Now(); working.After(endDate); working = working.Add(-1 * 24 * time.Hour) {
			log.Printf("Checking %s @ %s", currentPerson.Name, working.Format("2006-01-02"))

			err := try.Do(func(attempt int) (bool, error) {
				if attempt > 1 {
					time.Sleep(retryInterval * time.Duration(attempt-1) * time.Second)
				}
				resp, err = client.Get("https://" + domain + "/history/member/" + currentPerson.ID + working.Format("/2006/01/02"))
				if err != nil {
					return attempt < retryLimit, err
				}
				doc, err := goquery.NewDocumentFromResponse(resp)
				if err != nil {
					return attempt < retryLimit, err
				}
				forms := doc.Find("div.delete form")

				if forms.Size() > 0 {
					log.Printf("Found %d entries, deleting...", forms.Size())
				}

				forms.Each(func(i int, form *goquery.Selection) {
					delurl, exists := form.Attr("action")
					if !exists {
						log.Fatal("Can't find action for the delete form")
					}

					values := url.Values{}
					form.Find("input").Each(func(i int, input *goquery.Selection) {
						name, nameExists := input.Attr("name")
						value, valueExists := input.Attr("value")
						if nameExists && valueExists {
							values.Set(name, value)
						}
					})
					err := try.Do(func(attempt int) (bool, error) {
						if attempt > 1 {
							time.Sleep(retryInterval * time.Duration(attempt-1) * time.Second)
						}
						_, err := client.PostForm(delurl, values)
						return attempt < retryLimit, err
					})
					if err != nil {
						log.Fatalln("Tried", retryLimit, "times to delete post:", err)
					}
				})
				return attempt < retryLimit, err
			})

			if err != nil {
				log.Fatalln("Tried", retryLimit, "times to pull day:", err)
			}

		}
	}

}
