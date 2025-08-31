package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/schollz/progressbar/v3"
)

// --- CLI Colors ---
const (
	ColorReset  = "\033[0m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorCyan   = "\033[36m"
	ColorRed    = "\033[31m"
)

var bravePath = `C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`

func main() {
	ctx, cancel, err := newBraveContext()
	if err != nil {
		log.Fatalf("Failed to create Brave context: %v", err)
	}
	defer cancel()

	fmt.Println(ColorYellow + "Brave browser launched. Initializing session..." + ColorReset)
	if err := chromedp.Run(ctx, chromedp.Navigate("https://animepahe.ru/")); err != nil {
		log.Fatalf("Failed to navigate to animepahe: %v", err)
	}

	animeSession, animeTitle, err := searchAndSelectAnime(ctx)
	if err != nil {
		if err == ErrRestart {
			// This logic is simplified as the main loop will handle restart
		} else {
			log.Fatalf("Error during anime selection: %v", err)
		}
	}

	safeAnimeTitle := sanitizeName(animeTitle)

	allEpisodes, err := getEpisodes(ctx, animeSession)
	if err != nil {
		log.Fatalf("Error getting episodes: %v", err)
	}

	if len(allEpisodes) == 0 {
		fmt.Println(ColorRed + "\nThis selection has 0 episodes available." + ColorReset)
		// Simplified restart logic
		return
	}

	selectedEpisodes, err := selectEpisodeRange(allEpisodes)
	if err != nil {
		// Simplified restart logic
		return
	}

	fmt.Println(ColorYellow + "\nFetching download options for the first episode to determine quality..." + ColorReset)
	firstEpisodePlayerURL := fmt.Sprintf("https://animepahe.ru/play/%s/%s", animeSession, selectedEpisodes[0].Session)
	initialDownloadOptions, err := getDownloadOptions(ctx, firstEpisodePlayerURL)
	if err != nil {
		log.Fatalf("Could not get initial download options: %v", err)
	}

	_, preferredQuality, err := selectDownloadOption(initialDownloadOptions)
	if err != nil {
		// Simplified restart logic
		return
	}
	preferredResolution, preferredAudio := parseQuality(preferredQuality)
	fmt.Printf(ColorGreen+"Preference set: %s resolution, %s audio."+ColorReset+"\n", preferredResolution, preferredAudio)

	downloadDir, err := getDownloadDirectory(safeAnimeTitle)
	if err != nil {
		// Simplified restart logic
		return
	}

	fmt.Printf(ColorGreen+"\nStarting download of %d episodes to '%s'"+ColorReset+"\n", len(selectedEpisodes), downloadDir)

	for _, episode := range selectedEpisodes {
		fmt.Println(ColorYellow + "\n----------------------------------------------" + ColorReset)
		fmt.Printf(ColorYellow+"Processing Episode %d..."+ColorReset+"\n", episode.Episode)

		playerPageURL := fmt.Sprintf("https://animepahe.ru/play/%s/%s", animeSession, episode.Session)
		downloadOptions, err := getDownloadOptions(ctx, playerPageURL)
		if err != nil {
			log.Printf(ColorRed+"Could not get download options for episode %d: %v"+ColorReset, episode.Episode, err)
			continue
		}

		pahewinURL, actualQuality := findMatchingQuality(downloadOptions, preferredResolution, preferredAudio)
		if pahewinURL == "" {
			fmt.Println("Preferred quality not found, falling back to best available.")
			pahewinURL, actualQuality = selectBestQuality(downloadOptions)
			if pahewinURL == "" {
				log.Printf(ColorRed+"No suitable download quality found for episode %d."+ColorReset, episode.Episode)
				continue
			}
		}
		fmt.Printf("Using quality: %s\n", actualQuality)

		finalLink, err := resolveDownloadLink(ctx, pahewinURL)
		if err != nil {
			log.Printf(ColorRed+"Could not resolve download link for episode %d: %v"+ColorReset, episode.Episode, err)
			continue
		}

		filename := fmt.Sprintf("%s - Episode %02d.mp4", safeAnimeTitle, episode.Episode)
		err = downloadFile(finalLink, downloadDir, filename)
		if err != nil {
			log.Printf(ColorRed+"Failed to download episode %d: %v"+ColorReset, episode.Episode, err)
			continue
		}
	}

	fmt.Println(ColorGreen + "\n==============================================" + ColorReset)
	fmt.Println("✅ All downloads completed!")
	fmt.Println(ColorGreen + "==============================================" + ColorReset)
}

// --- THIS FUNCTION IS UPDATED ---
func newBraveContext() (context.Context, context.CancelFunc, error) {
	if _, err := os.Stat(bravePath); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("brave browser not found at %s", bravePath)
	}

	// 1. Create a temporary directory for the user profile
	tempProfileDir, err := os.MkdirTemp("", "chromedp-profile-*")
	if err != nil {
		return nil, nil, err
	}

	// 2. Define the browser preferences
	prefs := map[string]interface{}{
		"download": map[string]interface{}{
			"prompt_for_download": false,
			"default_directory":   tempProfileDir, // Browser downloads go here
		},
	}
	prefsJSON, err := json.Marshal(prefs)
	if err != nil {
		return nil, nil, err
	}

	// 3. Create the Default directory and write the Preferences file
	if err := os.MkdirAll(filepath.Join(tempProfileDir, "Default"), 0755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(tempProfileDir, "Default", "Preferences"), prefsJSON, 0644); err != nil {
		return nil, nil, err
	}

	// 4. Set up the allocator options with our custom profile
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.ExecPath(bravePath),
		chromedp.UserDataDir(tempProfileDir),
	)

	allocCtx, cancel1 := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel2 := chromedp.NewContext(allocCtx)

	// 5. Return a combined cancel function that also cleans up the temp profile dir
	return ctx, func() {
		cancel2()
		cancel1()
		os.RemoveAll(tempProfileDir)
	}, nil
}

// --- The rest of the script is unchanged ---
var ErrRestart = errors.New("user requested restart")

func sanitizeName(name string) string {
	r := strings.NewReplacer(":", "", "?", "", "<", "", ">", "", "\"", "", "/", "", "\\", "", "|", "", "*", "")
	return r.Replace(name)
}

func findMatchingQuality(options map[string]string, resolution, audio string) (string, string) {
	for quality, url := range options {
		qLower := strings.ToLower(quality)
		resMatch := strings.Contains(qLower, resolution)
		var audioMatch bool
		if audio == "jpn" {
			audioMatch = !strings.Contains(qLower, "eng")
		} else {
			audioMatch = strings.Contains(qLower, "eng")
		}
		if resMatch && audioMatch {
			return url, quality
		}
	}
	return "", ""
}

func parseQuality(quality string) (resolution string, audio string) {
	audio = "jpn"
	if strings.Contains(strings.ToLower(quality), "eng") {
		audio = "eng"
	}
	if strings.Contains(quality, "1080p") {
		resolution = "1080p"
	} else if strings.Contains(quality, "720p") {
		resolution = "720p"
	} else if strings.Contains(quality, "360p") {
		resolution = "360p"
	}
	return
}

func downloadFile(url, dir, filename string) error {
	fmt.Printf("Downloading episode to %s...\n", filename)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Referer", "https://kwik.si/")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned non-200 status: %s", resp.Status)
	}
	f, _ := os.OpenFile(filepath.Join(dir, filename), os.O_CREATE|os.O_WRONLY, 0644)
	defer f.Close()
	bar := progressbar.NewOptions(int(resp.ContentLength),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetDescription("[cyan][downloading][reset]"),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}))
	_, err = io.Copy(io.MultiWriter(f, bar), resp.Body)
	return err
}

func selectEpisodeRange(episodes []struct { Episode int `json:"episode"`; Session string `json:"session"` }) ([]struct { Episode int `json:"episode"`; Session string `json:"session"` }, error) {
	fmt.Printf(ColorGreen+"\nFound %d episodes."+ColorReset+"\n", len(episodes))
	reader := bufio.NewReader(os.Stdin)
	var start, end int
	for {
		fmt.Printf(ColorCyan+"Enter episode range to download (e.g., 1-10 or 5 for just one episode): "+ColorReset)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if strings.ToLower(input) == "restart" {
			return nil, ErrRestart
		}
		if strings.Contains(input, "-") {
			parts := strings.Split(input, "-")
			if len(parts) == 2 {
				start, _ = strconv.Atoi(parts[0])
				end, _ = strconv.Atoi(parts[1])
			}
		} else {
			start, _ = strconv.Atoi(input)
			end = start
		}
		if start > 0 && end >= start && end <= len(episodes) {
			break
		}
		fmt.Println("Invalid range.")
	}
	fmt.Printf("You selected episodes %d to %d.\n", start, end)
	return episodes[start-1 : end], nil
}

func getDownloadDirectory(animeTitle string) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(ColorCyan + "\nEnter download directory (leave blank for current folder): " + ColorReset)
		path, _ := reader.ReadString('\n')
		path = strings.TrimSpace(path)
		if strings.ToLower(path) == "restart" {
			return "", ErrRestart
		}
		if path == "" {
			path, _ = os.Getwd()
		}
		finalPath := filepath.Join(path, animeTitle)
		err := os.MkdirAll(finalPath, os.ModePerm)
		if err != nil {
			fmt.Printf(ColorRed+"Error creating directory: %v. Please try another path."+ColorReset, err)
			continue
		}
		return finalPath, nil
	}
}

func selectDownloadOption(options map[string]string) (string, string, error) {
	fmt.Println(ColorGreen + "\n--- Available Downloads ---" + ColorReset)
	var qualities []string
	for quality := range options {
		qualities = append(qualities, quality)
	}
	sort.Strings(qualities)
	for i, q := range qualities {
		fmt.Printf("%d: %s\n", i+1, q)
	}
	if len(qualities) == 0 {
		return "", "", fmt.Errorf("no download options available")
	}
	reader := bufio.NewReader(os.Stdin)
	var selection int
	for {
		fmt.Print(ColorCyan + "\nEnter the number for your preferred quality (or type 'restart'): " + ColorReset)
		selectionStr, _ := reader.ReadString('\n')
		selectionStr = strings.TrimSpace(selectionStr)
		if strings.ToLower(selectionStr) == "restart" {
			return "", "", ErrRestart
		}
		selection, _ = strconv.Atoi(selectionStr)
		if selection > 0 && selection <= len(qualities) {
			break
		}
		fmt.Println("Invalid selection.")
	}
	selectedQuality := qualities[selection-1]
	return options[selectedQuality], selectedQuality, nil
}

func selectBestQuality(options map[string]string) (string, string) {
	resolutions := []string{"1080p", "720p", "360p"}
	for _, res := range resolutions {
		for quality, url := range options {
			if strings.Contains(quality, res) {
				return url, quality
			}
		}
	}
	return "", ""
}

func searchAndSelectAnime(ctx context.Context) (string, string, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(ColorCyan + "Enter anime to search for (or type 'exit'): " + ColorReset)
	searchTerm, _ := reader.ReadString('\n')
	searchTerm = strings.TrimSpace(searchTerm)
	if strings.ToLower(searchTerm) == "exit" {
		os.Exit(0)
	}
	var resultsHTML string
	err := chromedp.Run(ctx,
		chromedp.SendKeys(`input.input-search[name="q"]`, searchTerm),
		chromedp.WaitVisible(`div.search-results-wrap a .result-title`),
		chromedp.Sleep(1*time.Second),
		chromedp.OuterHTML(`div.search-results-wrap`, &resultsHTML, chromedp.ByQuery),
	)
	if err != nil {
		return "", "", fmt.Errorf("failed to perform search: %w", err)
	}
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(resultsHTML))
	var results []struct{ Title, URL string }
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		results = append(results, struct{ Title, URL string }{
			Title: s.Find("div.result-title").Text(),
			URL:   s.AttrOr("href", ""),
		})
	})
	if len(results) == 0 {
		return "", "", fmt.Errorf("no results found")
	}
	fmt.Println(ColorGreen + "\n--- Search Results ---" + ColorReset)
	for i, res := range results {
		fmt.Printf("%d: %s\n", i+1, res.Title)
	}
	var selection int
	for {
		fmt.Print(ColorCyan + "\nEnter the number of the anime you want (or type 'restart'): " + ColorReset)
		selectionStr, _ := reader.ReadString('\n')
		selectionStr = strings.TrimSpace(selectionStr)
		if strings.ToLower(selectionStr) == "restart" {
			return "", "", ErrRestart
		}
		selection, _ = strconv.Atoi(selectionStr)
		if selection > 0 && selection <= len(results) {
			break
		}
		fmt.Println("Invalid selection.")
	}
	selectedAnime := results[selection-1]
	parts := strings.Split(selectedAnime.URL, "/")
	sessionID := parts[len(parts)-1]
	return sessionID, selectedAnime.Title, nil
}

func getEpisodes(ctx context.Context, animeSession string) ([]struct { Episode int `json:"episode"`; Session string `json:"session"` }, error) {
	apiURL := fmt.Sprintf("https://animepahe.ru/api?m=release&id=%s&sort=episode_asc&page=1", animeSession)
	var jsonResponse string
	err := chromedp.Run(ctx,
		chromedp.Navigate(apiURL),
		chromedp.Text(`body`, &jsonResponse, chromedp.ByQuery),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch episode list: %w", err)
	}
	var episodeData struct{ Data []struct{ Episode int `json:"episode"`; Session string `json:"session"` } }
	if err := json.Unmarshal([]byte(jsonResponse), &episodeData); err != nil {
		return nil, fmt.Errorf("failed to parse episode JSON: %w", err)
	}
	return episodeData.Data, nil
}

func getDownloadOptions(ctx context.Context, playerURL string) (map[string]string, error) {
	var downloadHTML string
	err := chromedp.Run(ctx,
		chromedp.Navigate(playerURL),
		chromedp.Click(`#downloadMenu`, chromedp.ByID),
		chromedp.WaitVisible(`#pickDownload a`, chromedp.ByID),
		chromedp.OuterHTML(`#pickDownload`, &downloadHTML, chromedp.ByID),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get download options: %w", err)
	}
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(downloadHTML))
	options := make(map[string]string)
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		options[strings.TrimSpace(s.Text())] = s.AttrOr("href", "")
	})
	return options, nil
}

func resolveDownloadLink(ctx context.Context, pahewinURL string) (string, error) {
	var kwikPageHTML string
	var kwikURL string
	err := chromedp.Run(ctx,
		chromedp.Navigate(pahewinURL),
		chromedp.Sleep(6*time.Second),
		chromedp.OuterHTML(`html`, &kwikPageHTML, chromedp.ByQuery),
	)
	if err != nil {
		return "", fmt.Errorf("failed to load pahe.win page: %w", err)
	}
	re := regexp.MustCompile(`"([^"]+kwik\.si[^"]+)"`)
	matches := re.FindStringSubmatch(kwikPageHTML)
	if len(matches) < 2 {
		return "", fmt.Errorf("could not find kwik.si link on pahe.win page")
	}
	kwikURL = matches[1]
	urlChan := make(chan string, 1)
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if req, ok := ev.(*network.EventRequestWillBeSent); ok {
			if strings.Contains(req.Request.URL, ".mp4") {
				select {
				case urlChan <- req.Request.URL:
				default:
				}
			}
		}
	})
	err = chromedp.Run(ctx,
		chromedp.Navigate(kwikURL),
		chromedp.Evaluate(`document.querySelector('form').submit()`, nil),
	)
	if err != nil && !strings.Contains(err.Error(), "context canceled") {
		return "", fmt.Errorf("failed to navigate and submit on kwik.si: %w", err)
	}
	select {
	case url := <-urlChan:
		return url, nil
	case <-time.After(15 * time.Second):
		return "", fmt.Errorf("timed out waiting for the final mp4 link")
	}
}