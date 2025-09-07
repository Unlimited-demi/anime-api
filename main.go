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

	// 🔥 initialize pool of Brave contexts
	tempDir := os.TempDir()
	number := 3 // <--- NEW NUMBER
	pool, err := downloader.NewBrowserPool(number, tempDir) // 3 contexts (tune as needed)
	if err != nil {
		log.Fatalf("❌ Failed to initialize browser pool: %v", err)
	}
	log.Printf("✅ Browser pool initialized with %d contexts", number) // <--- NEW LOG

	h := &downloader.Handler{Pool: pool}

	// register routes with CORS
	http.Handle("/search", corsMiddleware(http.HandlerFunc(h.SearchHandler)))
	http.Handle("/episodes", corsMiddleware(http.HandlerFunc(h.EpisodesHandler)))
	http.Handle("/download-options", corsMiddleware(http.HandlerFunc(h.DownloadOptionsHandler)))
	http.Handle("/download-link", corsMiddleware(http.HandlerFunc(h.DownloadLinkHandler)))
	http.Handle("/image-proxy", corsMiddleware(http.HandlerFunc(h.ImageProxyHandler)))
	http.Handle("/health", corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.Pool == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "Browser pool not initialized")
			return
		}
		fmt.Fprint(w, "OK")
	})))

	// periodic self-ping to keep Render alive
	go func() {
		selfURL := fmt.Sprintf("http://0.0.0.0:%s/health", port)
		for {
			time.Sleep(30 * time.Second)
			resp, err := http.Get(selfURL)
			if err != nil {
				log.Printf("⚠️ Health ping failed: %v", err)
				continue
			}
			_ = resp.Body.Close()
			log.Println("💓 Health ping OK")
		}
	}()

	// run server in main goroutine
	log.Printf("🚀 Server listening on 0.0.0.0:%s", port)
	if err := http.ListenAndServe("0.0.0.0:"+port, nil); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}
