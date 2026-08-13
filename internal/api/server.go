package api

import (
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/bgpls/bgpls/gen/bgpls/v1/bgplsv1connect"
	"github.com/bgpls/bgpls/internal/store"
	"github.com/bgpls/bgpls/internal/ui"
)

type Services struct {
	Topology  *TopologyService
	Path      *PathService
	History   *HistoryService
	Collector *CollectorService
}

func NewHandler(s *store.Store, peers PeerManager, version string, started time.Time, authorizer *Authorizer) http.Handler {
	services := Services{Topology: &TopologyService{Store: s}, Path: &PathService{Store: s}, History: &HistoryService{Store: s}, Collector: &CollectorService{Store: s, Peers: peers, Version: version, StartedAt: started}}
	mux := http.NewServeMux()
	options := []connect.HandlerOption{}
	path, handler := bgplsv1connect.NewTopologyServiceHandler(services.Topology, options...)
	mux.Handle(path, handler)
	path, handler = bgplsv1connect.NewPathServiceHandler(services.Path, options...)
	mux.Handle(path, handler)
	path, handler = bgplsv1connect.NewHistoryServiceHandler(services.History, options...)
	mux.Handle(path, handler)
	path, handler = bgplsv1connect.NewCollectorServiceHandler(services.Collector, options...)
	mux.Handle(path, handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"SERVING"}`))
	})
	mux.Handle("/ui/", http.StripPrefix("/ui/", ui.Handler()))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})
	var out http.Handler = mux
	if authorizer != nil {
		out = authorizer.Middleware(out)
	}
	return recoverMiddleware(out)
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
