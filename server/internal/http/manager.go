package http

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	webtransport "github.com/quic-go/webtransport-go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/internal/http/legacy"
	"github.com/m1k1o/neko/server/internal/media"
	"github.com/m1k1o/neko/server/internal/mediawebsocket"
	"github.com/m1k1o/neko/server/internal/mediawebtransport"
	"github.com/m1k1o/neko/server/pkg/types"
)

type HttpManagerCtx struct {
	logger       zerolog.Logger
	config       *config.Server
	router       types.Router
	http         *http.Server
	webTransport *webtransport.Server
}

func New(WebSocketManager types.WebSocketManager, MediaWebSocketManager *mediawebsocket.Manager, MediaManager *media.Manager, ApiManager types.ApiManager, config *config.Server) *HttpManagerCtx {
	logger := log.With().Str("module", "http").Logger()

	opts := []RouterOption{
		WithRequestID(), // create a request id for each request
	}

	// use real ip if behind proxy
	// before logger so it can log the real ip
	if config.Proxy {
		opts = append(opts, WithRealIP())
	}

	opts = append(opts,
		WithLogger(logger),
		WithRecoverer(), // recover from panics without crashing server
	)

	if config.HasCors() {
		opts = append(opts, WithCORS(config.AllowOrigin))
	}

	if config.PathPrefix != "/" {
		opts = append(opts, WithPathPrefix(config.PathPrefix))
	}

	router := newRouter(opts...)

	router.Route("/api", ApiManager.Route)

	router.Get("/api/ws", WebSocketManager.Upgrade(func(r *http.Request) bool {
		return config.AllowOrigin(r.Header.Get("Origin"))
	}))
	router.Get("/media/ws", MediaWebSocketManager.Upgrade(func(r *http.Request) bool {
		return config.AllowOrigin(r.Header.Get("Origin"))
	}))

	batch := batchHandler{
		Router:     router,
		PathPrefix: "/api",
		Excluded: []string{
			"/api/batch", // do not allow batchception
			"/api/ws",
		},
	}
	router.Post("/api/batch", batch.Handle)

	router.Get("/health", func(w http.ResponseWriter, r *http.Request) error {
		_, err := w.Write([]byte("true"))
		return err
	})

	if config.Metrics {
		router.Get("/metrics", func(w http.ResponseWriter, r *http.Request) error {
			promhttp.Handler().ServeHTTP(w, r)
			return nil
		})
	}

	if config.Static != "" {
		fs := http.FileServer(http.Dir(config.Static))
		router.Get("/*", func(w http.ResponseWriter, r *http.Request) error {
			_, err := os.Stat(config.Static + r.URL.Path)
			if err == nil {
				fs.ServeHTTP(w, r)
				return nil
			}
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return nil
			}
			return err
		})
	}

	if config.PProf {
		pprofHandler(router)
	}

	manager := &HttpManagerCtx{
		logger: logger,
		config: config,
		router: router,
		http: &http.Server{
			Addr:    config.Bind,
			Handler: router,
		},
	}
	manager.webTransport = newMediaWebTransport(config, MediaManager)
	return manager
}

func newMediaWebTransport(config *config.Server, manager *media.Manager) *webtransport.Server {
	if config.Cert == "" || config.Key == "" {
		return nil
	}

	h3 := &http3.Server{
		Addr:      config.Bind,
		TLSConfig: http3.ConfigureTLSConfig(&tls.Config{}),
		QUICConfig: &quic.Config{
			KeepAlivePeriod: 10 * time.Second,
		},
	}
	server := &webtransport.Server{
		H3:          h3,
		CheckOrigin: func(request *http.Request) bool { return config.AllowOrigin(request.Header.Get("Origin")) },
	}
	webtransport.ConfigureHTTP3Server(h3)
	mux := http.NewServeMux()
	mux.Handle(path.Join(config.PathPrefix, "/media/wt"), mediawebtransport.New(manager, server).Handler())
	h3.Handler = mux
	return server
}

func (manager *HttpManagerCtx) Start() {
	if manager.config.Cert != "" && manager.config.Key != "" {
		go func() {
			if err := manager.http.ListenAndServeTLS(manager.config.Cert, manager.config.Key); err != http.ErrServerClosed {
				manager.logger.Panic().Err(err).Msg("unable to start https server")
			}
		}()
		manager.logger.Info().Msgf("https listening on %s", manager.http.Addr)

		go func() {
			if err := manager.webTransport.ListenAndServeTLS(manager.config.Cert, manager.config.Key); err != nil &&
				!errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
				manager.logger.Panic().Err(err).Msg("unable to start webtransport server")
			}
		}()
		manager.logger.Info().Msgf("webtransport listening on udp %s", manager.http.Addr)

		// if we have legacy mode, we need to start local http server too
		if viper.GetBool("legacy") {
			// create a listener for the API server with a random port
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				manager.logger.Panic().Err(err).Msg("unable to start legacy http proxy")
			}

			go func() {
				if err := http.Serve(listener, manager.router); err != http.ErrServerClosed {
					manager.logger.Panic().Err(err).Msg("unable to start http server")
				}
			}()
			manager.logger.Info().Msgf("legacy proxy listening on %s", listener.Addr().String())

			legacy.New(listener.Addr().String(), manager.config.PathPrefix).Route(manager.router)
		}
	} else {
		go func() {
			if err := manager.http.ListenAndServe(); err != http.ErrServerClosed {
				manager.logger.Panic().Err(err).Msg("unable to start http server")
			}
		}()
		manager.logger.Info().Msgf("http listening on %s", manager.http.Addr)

		// start legacy proxy if enabled
		if viper.GetBool("legacy") {
			legacy.New(manager.http.Addr, manager.config.PathPrefix).Route(manager.router)
		}
	}
}

func (manager *HttpManagerCtx) Shutdown() error {
	manager.logger.Info().Msg("shutdown")

	var webTransportErr error
	if manager.webTransport != nil {
		webTransportErr = manager.webTransport.Close()
	}
	return errors.Join(manager.http.Shutdown(context.Background()), webTransportErr)
}
