package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"anime-api/downloader"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	h := &downloader.Handler{}

	// register routes
	http.HandleFunc("/search", h.SearchHandler)
	http.HandleFunc("/episodes", h.EpisodesHandler)
	http.HandleFunc("/download-options", h.DownloadOptionsHandler)
	http.HandleFunc("/download-link", h.DownloadLinkHandler)
	http.HandleFunc("/image-proxy", h.ImageProxyHandler)

	// health endpoint defined inline
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if h.BrowserContext == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "Browser not initialized")
			return
		}
		fmt.Fprint(w, "OK")
	})

	// init browser in background
	go func() {
		log.Println("⏳ Initializing Brave/Chromium browser context...")
		tempDir := os.TempDir()
		ctx, _, err := downloader.NewBraveContext(tempDir)
		if err != nil {
			log.Printf("⚠️ Browser init failed: %v", err)
			return
		}
		h.BrowserContext = ctx
		h.InitSession()
		log.Println("✅ Browser has been initialized and session is ready.")
		// cancel() when shutting down
	}()

	// periodic self-ping to keep Render alive
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			resp, err := http.Get("http://localhost:" + port + "/health")
			if err != nil {
				log.Printf("⚠️ Health ping failed: %v", err)
				continue
			}
			_ = resp.Body.Close()
			log.Println("💓 Health ping OK")
		}
	}()

	// run server in main goroutine (so Render detects it immediately)
	log.Printf("🚀 Server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}
