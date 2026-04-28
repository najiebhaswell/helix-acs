package cwmp

import (
	"context"
	"net/http"
	"time"

	"github.com/raykavin/helix-acs/internal/auth"
	"github.com/raykavin/helix-acs/internal/logger"
)

// Server is a thin HTTP wrapper around the CWMP session Handler. It wires
// together Digest authentication, body-size limiting, and request routing so
// that Handler can stay focused on protocol logic.
type Server struct {
	handler    *Handler
	digestAuth *auth.DigestAuth
	log        logger.Logger
}

// NewServer creates a Server from its three dependencies.
func NewServer(handler *Handler, digestAuth *auth.DigestAuth, log logger.Logger) *Server {
	return &Server{
		handler:    handler,
		digestAuth: digestAuth,
		log:        log,
	}
}

// Router returns the http.Handler for the CWMP endpoint. It mounts the session
// Handler at POST /acs, wrapped with Digest auth and a 1 MB body cap.
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// Wrap the core handler: enforce Digest auth then delegate.
	cwmpHandler := s.digestAuth.Middleware(http.HandlerFunc(s.handler.ServeHTTP))
	cwmpHandler = limitBody(cwmpHandler, 16<<20) // 16 MB

	mux.Handle("/", cwmpHandler)
	mux.Handle("/acs", cwmpHandler)
	mux.Handle("/acs/", cwmpHandler)

	s.log.Debug("CWMP: Router mounted at /acs")
	return mux
}

// StartPresenceMonitor runs a background goroutine that periodically marks
// devices offline when they have not sent an Inform within the expected window.
// The stale threshold is informInterval * multiplier (default 3×) to absorb
// normal network jitter. Call this once after the server is constructed.
func (s *Server) StartPresenceMonitor(ctx context.Context) {
	h := s.handler
	if h.informInterval <= 0 {
		s.log.Warn("CWMP: informInterval not set, presence monitor disabled")
		return
	}

	// Allow up to 3 missed Informs before declaring a device offline.
	staleThreshold := h.informInterval * 3
	// Check every half inform-interval for responsiveness.
	ticker := time.NewTicker(h.informInterval / 2)

	s.log.WithField("threshold", staleThreshold).
		WithField("check_interval", h.informInterval/2).
		Info("CWMP: presence monitor started")

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.log.Info("CWMP: presence monitor stopped")
				return
			case <-ticker.C:
				cutoff := time.Now().UTC().Add(-staleThreshold)
				count, err := h.deviceSvc.MarkStaleOffline(ctx, cutoff)
				if err != nil {
					s.log.WithError(err).Warn("CWMP: presence monitor error")
				} else if count > 0 {
					s.log.WithField("count", count).
						Info("CWMP: devices marked offline by presence monitor")
				}
			}
		}
	}()
}

// limitBody wraps next so that request bodies larger than maxBytes are
// rejected with 413 before the handler reads anything.
func limitBody(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}
