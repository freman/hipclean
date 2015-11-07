package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"golang.org/x/crypto/ssh/terminal"
)

var terminalState *terminal.State

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func init() {
	if runtime.GOOS != "windows" {
		terminalState, _ := terminal.GetState(0)
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		go func() {
			<-sigint
			terminal.Restore(0, terminalState)
			fmt.Println("")
			os.Exit(1)
		}()
	}
}

func mustParseResponse(resp *http.Response) *goquery.Document {
	gq, err := goquery.NewDocumentFromResponse(resp)
	if err != nil {
		log.Fatal(err)
	}
	return gq
}

func parseSignupDate(s string) time.Time {
	// Dates ont he page are either "n days ago" or "day longmonth year"
	var date time.Time
	if strings.Contains(s, "days ago") {
		days, _ := strconv.Atoi(strings.Split(s, " ")[0])
		date = time.Now().Add(-24 * time.Duration(days) * time.Hour)
	} else {
		date, _ = time.Parse("2 January 2006", s)
	}
	return date
}
