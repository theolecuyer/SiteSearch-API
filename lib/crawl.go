package lib

import (
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kljensen/snowball"
)

type Indexes interface {
	AddToIndex(url string, currWords []string)
	Search(query string) hits
}

func Crawl(baseURL string, index Indexes) {
	downloadRoutines := 1
	indexRoutines := 1
	visitedUrls := make(map[string]bool) //Make a map for all visited urls
	host, err := url.Parse(baseURL)
	if err != nil {
		log.Printf("URL Parse returned %v", err)
	}
	visitedUrls[baseURL] = true
	hostName := host.Host
	//Read the robots.txt file if it exists
	crawlDelay, dissalowList := loadRobots(hostName)
	chDownload := make(chan string, 1000)
	chExtract := make(chan downloadResults, 1000)
	var mu sync.Mutex     //Make a mutex for the visited map
	chDownload <- baseURL //Add the first url

	for i := 0; i < downloadRoutines; i++ {
		go downloadWorker(chDownload, chExtract, dissalowList, crawlDelay)
	}
	for i := 0; i < indexRoutines; i++ {
		go indexWorker(chDownload, chExtract, index, baseURL, hostName, visitedUrls, &mu)
	}

	//Wait for intial goroutines to spin up and call others
	time.Sleep(2 * time.Second)

	//Loop to check the channel content, if they are empty close them and their goroutines will end
	for {
		time.Sleep(1 * time.Second)
		if len(chDownload) == 0 && len(chExtract) == 0 {
			break
		}
	}
	close(chDownload)
	close(chExtract)
	fmt.Printf("All goroutines finished")
}

func avgTime(avgMessage string, times []time.Duration) {
	var total float64
	var amt float64
	for _, value := range times {
		total += float64(value.Milliseconds())
		amt += 1
	}
	fmt.Printf("The average time for %s is %vms\n", avgMessage, (total / amt))
}

func downloadWorker(chDownload chan string, chExtract chan downloadResults, dissalowList map[string]bool, crawlDelay float64) {
	allDownloadTimes := []time.Duration{}
	for currentUrl := range chDownload {
		startTime := time.Now()
		allowed := true
		for dissalowedPath := range dissalowList {
			matched, _ := regexp.MatchString(dissalowedPath, currentUrl)
			if matched {
				allowed = false
				break
			}
		}
		if allowed {
			Download(currentUrl, chExtract)
			time.Sleep(time.Duration(crawlDelay) * time.Second)
		}
		allDownloadTimes = append(allDownloadTimes, time.Since(startTime))
	}
	avgTime("Download", allDownloadTimes)
}

func indexWorker(chDownload chan string, chExtract chan downloadResults, index Indexes, baseURL string, hostName string, visitedUrls map[string]bool, mu *sync.Mutex) {
	allIndexTimes := []time.Duration{}
	for content := range chExtract {
		startTime := time.Now()
		words, hrefs := Extract(content.data)
		currentWords := []string{}
		for _, word := range words {
			if stemmedWord, err := snowball.Stem(word, "english", true); err != nil {
				log.Printf("Snowball error: %v", err)
			} else {
				currentWords = append(currentWords, stemmedWord)
			}
		}
		links := Clean(baseURL, hrefs)
		for _, cleanedURL := range links {
			mu.Lock()
			if !visitedUrls[cleanedURL.String()] && hostName == cleanedURL.Host {
				chDownload <- cleanedURL.String()
				visitedUrls[cleanedURL.String()] = true
			}
			mu.Unlock()
		}
		allIndexTimes = append(allIndexTimes, time.Since(startTime))
		index.AddToIndex(content.url, currentWords)
	}
	avgTime("Index", allIndexTimes)
}

func loadRobots(hostName string) (float64, map[string]bool) {
	//Set the default crawl delay as 100 ms
	var crawlDelay float64 = 0.1
	robotsUrl := "http://" + hostName + "/robots.txt"
	dissalowList := make(map[string]bool)
	if res, err := downloadRobots(robotsUrl); err != nil {
		log.Println("No robots file found, continuing standard crawling")
	} else {
		lines := strings.Split(res, "\n")
		currUser := false
		for i := range lines {
			if strings.HasPrefix(lines[i], "User-agent:") {
				if strings.HasPrefix(lines[i], "User-agent: *") {
					currUser = true
				} else {
					currUser = false
				}
			} else if currUser && strings.HasPrefix(lines[i], "Disallow:") {
				filePath := strings.TrimSpace(strings.TrimPrefix(lines[i], "Disallow:"))
				dissalowed := strings.ReplaceAll(filePath, "*", ".*")
				dissalowList[dissalowed] = false
			} else if strings.HasPrefix(lines[i], "Crawl-delay:") {
				delay := strings.TrimSpace(strings.TrimPrefix(lines[i], "Crawl-delay:"))
				i, err := strconv.ParseFloat(delay, 64)
				if err != nil {
					log.Println("robots.txt crawl delay incorrectly formatted")
				} else {
					crawlDelay = float64(i)
				}
			}
		}
	}
	return crawlDelay, dissalowList
}
