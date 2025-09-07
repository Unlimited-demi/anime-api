package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// ---------------- Types ----------------

type Handler struct {
	Pool *BrowserPool
}

type BrowserPool struct {
	pool chan context.Context
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
	Total       int       `json:"total"`
	PerPage     int       `json:"per_page"`
	CurrentPage int       `json:"current_page"`
	LastPage    int       `json:"last_page"`
	Data        []Episode `json:"data"`
}

// ---------------- Brave Init ----------------

// findBravePath detects Brave binary depending on OS
func findBravePath() string {
	var possiblePaths []string

	if runtime.GOOS == "windows" {
		possiblePaths = []string{
			`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
			`C:\Program Files (x86)\BraveSoftware\Brave-Browser\Application\brave.exe`,
		}
	} else {
		possiblePaths = []string{
			"/usr/bin/brave-browser",
			"/usr/bin/brave",
			"/opt/brave.com/brave/brave-browser",
		}
	}

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	log.Fatal("❌ Brave browser not found in common paths")
	return ""
}

// NewBraveContext creates a new Brave-powered chromedp context
func NewBraveContext(tmpDir string) (context.Context, context.CancelFunc, error) {
	bravePath := findBravePath()

	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(bravePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.UserDataDir(filepath.Join(tmpDir, fmt.Sprintf("brave-profile-%d", time.Now().UnixNano()))),
	)

	allocCtx, _ := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)

	// Ensure the browser session is actually alive
	if err := chromedp.Run(ctx, page.Enable()); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to start Brave context: %w", err)
	}

	log.Println("✅ Brave browser context initialized")
	return ctx, cancel, nil
}

// ---------------- Pool ----------------

func NewBrowserPool(size int, tmpDir string) (*BrowserPool, error) {
	pool := make(chan context.Context, size)
	for i := 0; i < size; i++ {
		ctx, _, err := NewBraveContext(tmpDir)
		if err != nil {
			return nil, err
		}
		// warm session
		if err := chromedp.Run(ctx, chromedp.Navigate("https://animepahe.ru/")); err != nil {
			log.Printf("⚠️ Failed to warm context %d: %v", i, err)
		} else {
			log.Printf("🔥 Browser context %d warmed", i+1)
		}
		pool <- ctx
	}
	return &BrowserPool{pool: pool}, nil
}

func (bp *BrowserPool) Get() context.Context {
	return <-bp.pool
}

func (bp *BrowserPool) Put(ctx context.Context) {
	bp.pool <- ctx
}

// ---------------- Handlers ----------------

func (h *Handler) SearchHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, `Missing search query parameter "q"`, http.StatusBadRequest)
		return
	}
	log.Printf("API: Received search request for '%s'", query)

	ctx := h.Pool.Get()
	defer h.Pool.Put(ctx)

	var resultsHTML string
	err := chromedp.Run(ctx,
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

	ctx := h.Pool.Get()
	defer h.Pool.Put(ctx)

	allEpisodes := []Episode{}
	currentPage := 1
	lastPage := 1

	for currentPage <= lastPage {
		apiURL := fmt.Sprintf("https://animepahe.ru/api?m=release&id=%s&sort=episode_asc&page=%d", session, currentPage)
		var jsonResponse string
		err := chromedp.Run(ctx,
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

	ctx := h.Pool.Get()
	defer h.Pool.Put(ctx)

	playerPageURL := fmt.Sprintf("https://animepahe.ru/play/%s/%s", animeSession, episodeSession)

	downloadOptions, err := h.getDownloadOptions(ctx, playerPageURL)
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

	ctx := h.Pool.Get()
	defer h.Pool.Put(ctx)

	finalLink, err := h.resolveDownloadLink(ctx, pahewinURL)
	if err != nil {
		http.Error(w, "Failed to resolve final download link: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": finalLink})
}

// ---------------- Internals ----------------

func (h *Handler) getDownloadOptions(ctx context.Context, playerURL string) (map[string]string, error) {
	var downloadHTML string
	err := chromedp.Run(ctx,
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

func (h *Handler) resolveDownloadLink(ctx context.Context, pahewinURL string) (string, error) {
	var kwikPageHTML string
	var kwikURL string
	err := chromedp.Run(ctx,
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
	listenCtx, cancelListen := context.WithCancel(ctx)
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
	err = chromedp.Run(ctx,
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


func (h *Handler) ImageProxyHandler(w http.ResponseWriter, r *http.Request) {
	imageURL := r.URL.Query().Get("url")
	if imageURL == "" {
		http.Error(w, "Missing image URL parameter 'url'", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequest("GET", imageURL, nil)
	if err != nil {
		http.Error(w, "Failed to create image request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Referer", "https://animepahe.ru/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "Failed to fetch image", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Content-Length", resp.Header.Get("Content-Length"))
	io.Copy(w, resp.Body)
}
