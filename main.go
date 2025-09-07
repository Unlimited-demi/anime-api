package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"anime-api/downloader"
)

// corsMiddleware allows cross-origin requests from anywhere (or restrict to your frontend later)
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}



func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	h := &downloader.Handler{}

	// register routes
	http.Handle("/search", corsMiddleware(http.HandlerFunc(h.SearchHandler)))
	http.Handle("/episodes", corsMiddleware(http.HandlerFunc(h.EpisodesHandler)))
	http.Handle("/download-options", corsMiddleware(http.HandlerFunc(h.DownloadOptionsHandler)))
	http.Handle("/download-link", corsMiddleware(http.HandlerFunc(h.DownloadLinkHandler)))
	http.Handle("/image-proxy", corsMiddleware(http.HandlerFunc(h.ImageProxyHandler)))
	http.Handle("/health", corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.BrowserContext == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "Browser not initialized")
			return
		}
		fmt.Fprint(w, "OK")
	})))


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
		time.Sleep(10 * time.Second)
		resp, err := http.Get("https://anime-api-nb3u.onrender.com/health")
		if err != nil {
			log.Printf("⚠️ Health ping failed: %v", err)
			continue
		}
		_ = resp.Body.Close()
		log.Println("💓 Health ping OK")
	}
	}()


	// run server in main goroutine (so Render detects it immediately)
	log.Printf("🚀 Server listening on 0.0.0.0:%s", port)
	if err := http.ListenAndServe("0.0.0.0:"+port, nil); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}
