package downloader

import (
	"context"
	"log"
	"sync"

	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
)

type SessionManager struct {
	mu            sync.Mutex
	masterContext context.Context
	sessions      map[string]context.Context
	cancelFuncs   map[string]context.CancelFunc
}

func NewSessionManager(masterCtx context.Context) *SessionManager {
	return &SessionManager{
		masterContext: masterCtx,
		sessions:      make(map[string]context.Context),
		cancelFuncs:   make(map[string]context.CancelFunc),
	}
}

func (sm *SessionManager) NewSession() (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sessionID := uuid.New().String()
	ctx, cancel := chromedp.NewContext(sm.masterContext)

	if err := chromedp.Run(ctx, chromedp.Navigate("https://animepahe.ru/")); err != nil {
		cancel()
		return "", err
	}

	sm.sessions[sessionID] = ctx
	sm.cancelFuncs[sessionID] = cancel
	log.Printf("Created new session: %s", sessionID)
	return sessionID, nil
}

func (sm *SessionManager) GetSession(sessionID string) (context.Context, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	ctx, ok := sm.sessions[sessionID]
	return ctx, ok
}

func (sm *SessionManager) CloseSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if cancel, ok := sm.cancelFuncs[sessionID]; ok {
		cancel()
		delete(sm.sessions, sessionID)
		delete(sm.cancelFuncs, sessionID)
		log.Printf("Closed session: %s", sessionID)
	}
}