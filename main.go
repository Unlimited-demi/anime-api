package main

import (
	"anime-api/downloader"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/rs/cors"
)

func main() {
	tempDownloadDir, err := os.MkdirTemp("", "chromedp-profile-*")
	if err != nil {
		log.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDownloadDir)

	ctx, cancel, err := downloader.NewBraveContext(tempDownloadDir)
	if err != nil {
		log.Fatalf("Failed to create Brave context: %v", err)
	}
	defer cancel()

	handler := &downloader.Handler{
		BrowserContext: ctx,
	}

	fmt.Println("Initializing browser session...")
	handler.InitSession()

	mux := http.NewServeMux()
	mux.HandleFunc("/search", handler.SearchHandler)
	mux.HandleFunc("/episodes", handler.EpisodesHandler)
	mux.HandleFunc("/download-options", handler.DownloadOptionsHandler)
	mux.HandleFunc("/download-link", handler.DownloadLinkHandler)
	mux.HandleFunc("/image-proxy", handler.ImageProxyHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	if os.Getenv("RENDER") == "true" {
		go func() {
			time.Sleep(10 * time.Second)
			serviceURL := os.Getenv("RENDER_EXTERNAL_URL")
			if serviceURL == "" {
				log.Println("Warning: RENDER_EXTERNAL_URL not set. Self-pinging disabled.")
				return
			}
			healthCheckURL := serviceURL + "/health"

			log.Println("Starting self-ping routine to keep service alive at:", healthCheckURL)
			ticker := time.NewTicker(20 * time.Second)
			defer ticker.Stop()

			for range ticker.C {
				log.Println("Pinging self...")
				resp, err := http.Get(healthCheckURL)
				if err != nil {
					log.Printf("Self-ping failed: %v", err)
					continue
				}
				resp.Body.Close()
				log.Printf("Self-ping successful: Status %s", resp.Status)
			}
		}()
	}

	corsHandler := cors.Default().Handler(mux)

	fmt.Println("✅ API server starting on http://localhost:8080")
	if err := http.ListenAndServe(":8080", corsHandler); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}