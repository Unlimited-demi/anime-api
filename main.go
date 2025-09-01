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
	handler := downloader.NewHandler()

	go handler.InitBrowser()

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

	go func() {
		time.Sleep(30 * time.Second)
		healthCheckURL := "https://anime-api-nb3u.onrender.com/health"

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

	corsHandler := cors.Default().Handler(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println("✅ API server starting on http://localhost:" + port)
	if err := http.ListenAndServe(":"+port, corsHandler); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}