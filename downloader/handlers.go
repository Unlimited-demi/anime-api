package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

var bravePath = `C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`

type Handler struct {
	BrowserContext context.Context
}

type Anime struct {
	Title    string `json:"title"`
	Session  string `json:"session"`
	Poster   string `json:"poster"`
	Type     string `json:"type"`
	Episodes string `json:"episodes"`
	Season   string `json:"season"`
	Year     string `json:"year"`
}

type Episode struct {
	Episode   int    `json:"episode"`
	Session   string `json:"session"`
	Duration  string `json:"duration"`
	Snapshot  string `json:"snapshot"`
	Audio     string `json:"audio"`
	CreatedAt string `json:"created_at"`
}

type EpisodeAPIResponse struct {
	Total      int       `json:"total"`
	PerPage    int       `json:"per_page"`
	CurrentPage int      `json:"current_page"`
	LastPage   int       `json:"last_page"`
	Data       []Episode `json:"data"`
}

func (h *Handler) SearchHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, `Missing search query parameter "q"`, http.StatusBadRequest)
		return
	}
	log.Printf("API: Received search request for '%s'", query)

	var resultsHTML string
	err := chromedp.Run(h.BrowserContext,
		chromedp.Navigate("https://animepahe.ru/"),
		chromedp.SendKeys(`input.input-search[name="q"]`, query),
		chromedp.WaitVisible(`div.search-results-wrap a .result-title`),
		chromedp.Sleep(1*time.Second),
		chromedp.OuterHTML(`div.search-results-wrap`, &resultsHTML, chromedp.ByQuery),
	)
	if err != nil {
		http.Error(w, "Failed to perform search", http.StatusInternalServerError)
		return
	}

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(resultsHTML))
	var results []Anime
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		href := s.AttrOr("href", "")
		parts := strings.Split(href, "/")
		statusText := s.Find("div.result-status").Text()
		statusParts := strings.Split(statusText, " - ")
		animeType := ""
		episodes := ""
		if len(statusParts) > 0 {
			animeType = statusParts[0]
		}
		if len(statusParts) > 1 {
			episodes = strings.TrimSpace(statusParts[1])
		}

		results = append(results, Anime{
			Title:    s.Find("div.result-title").Text(),
			Session:  parts[len(parts)-1],
			Poster:   s.Find("img").AttrOr("src", ""),
			Type:     animeType,
			Episodes: episodes,
			Year:     s.Find("div.result-season").Text(),
		})
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (h *Handler) EpisodesHandler(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if session == "" {
		http.Error(w, `Missing anime session ID parameter "session"`, http.StatusBadRequest)
		return
	}
	log.Printf("API: Received episode list request for session '%s'", session)

	allEpisodes := []Episode{}
	currentPage := 1
	lastPage := 1

	for currentPage <= lastPage {
		apiURL := fmt.Sprintf("https://animepahe.ru/api?m=release&id=%s&sort=episode_asc&page=%d", session, currentPage)
		var jsonResponse string
		err := chromedp.Run(h.BrowserContext,
			chromedp.Navigate(apiURL),
			chromedp.Text(`body`, &jsonResponse, chromedp.ByQuery),
		)
		if err != nil {
			http.Error(w, "Failed to fetch episode list page", http.StatusInternalServerError)
			return
		}

		var apiData EpisodeAPIResponse
		if err := json.Unmarshal([]byte(jsonResponse), &apiData); err != nil {
			http.Error(w, "Failed to parse episode JSON", http.StatusInternalServerError)
			return
		}

		allEpisodes = append(allEpisodes, apiData.Data...)
		lastPage = apiData.LastPage
		currentPage++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(allEpisodes)
}

func (h *Handler) DownloadOptionsHandler(w http.ResponseWriter, r *http.Request) {
	animeSession := r.URL.Query().Get("anime_session")
	episodeSession := r.URL.Query().Get("episode_session")
	if animeSession == "" || episodeSession == "" {
		http.Error(w, `Missing "anime_session" or "episode_session" parameter`, http.StatusBadRequest)
		return
	}
	log.Printf("API: Received download options request for anime %s, episode %s", animeSession, episodeSession)

	playerPageURL := fmt.Sprintf("https://animepahe.ru/play/%s/%s", animeSession, episodeSession)

	downloadOptions, err := h.getDownloadOptions(playerPageURL)
	if err != nil {
		http.Error(w, "Failed to get download options: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(downloadOptions)
}

func (h *Handler) DownloadLinkHandler(w http.ResponseWriter, r *http.Request) {
	pahewinURL := r.URL.Query().Get("pahewin_url")
	if pahewinURL == "" {
		http.Error(w, `Missing "pahewin_url" parameter`, http.StatusBadRequest)
		return
	}
	log.Printf("API: Received download link request for %s", pahewinURL)

	finalLink, err := h.resolveDownloadLink(pahewinURL)
	if err != nil {
		http.Error(w, "Failed to resolve final download link: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": finalLink})
}

func (h *Handler) InitSession() {
	if err := chromedp.Run(h.BrowserContext,
		chromedp.Navigate("https://animepahe.ru/"),
	); err != nil {
		log.Fatalf("Failed to navigate to animepahe: %v", err)
	}
	if err := chromedp.Run(h.BrowserContext, chromedp.ActionFunc(func(ctx context.Context) error {
		return page.SetInterceptFileChooserDialog(true).Do(ctx)
	})); err != nil {
		log.Printf("Warning: could not set file chooser interception: %v", err)
	}
	log.Println("Browser session initialized.")
}

func NewBraveContext(tempDownloadDir string) (context.Context, context.CancelFunc, error) {
	if _, err := os.Stat(bravePath); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("brave browser not found at %s", bravePath)
	}

	prefs := map[string]interface{}{
		"download": map[string]interface{}{
			"prompt_for_download": false,
			"default_directory":   tempDownloadDir,
		},
	}
	prefsJSON, err := json.Marshal(prefs)
	if err != nil {
		return nil, nil, err
	}

	tempProfileDir, err := os.MkdirTemp("", "chromedp-profile-*")
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Join(tempProfileDir, "Default"), 0755); err != nil {
		os.RemoveAll(tempProfileDir)
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(tempProfileDir, "Default", "Preferences"), prefsJSON, 0644); err != nil {
		os.RemoveAll(tempProfileDir)
		return nil, nil, err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.ExecPath(bravePath),
		chromedp.UserDataDir(tempProfileDir),
	)

	allocCtx, cancel1 := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel2 := chromedp.NewContext(allocCtx)

	return ctx, func() {
		cancel2()
		cancel1()
		os.RemoveAll(tempProfileDir)
	}, nil
}

func (h *Handler) getDownloadOptions(playerURL string) (map[string]string, error) {
	var downloadHTML string
	err := chromedp.Run(h.BrowserContext,
		chromedp.Navigate(playerURL),
		chromedp.Click(`#downloadMenu`, chromedp.ByID),
		chromedp.WaitVisible(`#pickDownload a`, chromedp.ByID),
		chromedp.OuterHTML(`#pickDownload`, &downloadHTML, chromedp.ByID),
	)
	if err != nil {
		return nil, err
	}
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(downloadHTML))
	options := make(map[string]string)
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		options[strings.TrimSpace(s.Text())] = s.AttrOr("href", "")
	})
	return options, nil
}

func (h *Handler) resolveDownloadLink(pahewinURL string) (string, error) {
	var kwikPageHTML string
	var kwikURL string
	err := chromedp.Run(h.BrowserContext,
		chromedp.Navigate(pahewinURL),
		chromedp.Sleep(6*time.Second),
		chromedp.OuterHTML(`html`, &kwikPageHTML, chromedp.ByQuery),
	)
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(`"([^"]+kwik\.si[^"]+)"`)
	matches := re.FindStringSubmatch(kwikPageHTML)
	if len(matches) < 2 {
		return "", fmt.Errorf("could not find kwik.si link on pahe.win page")
	}
	kwikURL = matches[1]
	urlChan := make(chan string, 1)
	listenCtx, cancelListen := context.WithCancel(h.BrowserContext)
	defer cancelListen()
	chromedp.ListenTarget(listenCtx, func(ev interface{}) {
		if req, ok := ev.(*network.EventRequestWillBeSent); ok {
			if strings.Contains(req.Request.URL, ".mp4") {
				select {
				case urlChan <- req.Request.URL:
					cancelListen()
				default:
				}
			}
		}
	})
	err = chromedp.Run(h.BrowserContext,
		chromedp.Navigate(kwikURL),
		chromedp.Evaluate(`document.querySelector('form').submit()`, nil),
	)
	if err != nil && !strings.Contains(err.Error(), "context canceled") {
		return "", err
	}
	select {
	case url := <-urlChan:
		return url, nil
	case <-time.After(15 * time.Second):
		return "", fmt.Errorf("timed out waiting for the final mp4 link")
	}
}