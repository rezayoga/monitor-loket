package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/NYTimes/gziphandler"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/thedevsaddam/renderer"
	"monitor-loket/config"
	"net/http"
	"strings"
	"time"
)

func (app *application) routes() http.Handler {
	// create a router mux
	mux := chi.NewRouter()
	rnd := renderer.New()
	//var api = "/api"

	mux.Use(app.middleware)
	mux.Use(middleware.RequestID)
	mux.Use(middleware.RealIP)
	mux.Use(middleware.Recoverer)
	mux.Use(middleware.Logger)
	mux.Use(gziphandler.GzipHandler)
	mux.Use(app.prometheusMiddleware)

	// CSRF protection middleware
	csrfMiddleware := csrf.Protect(
		[]byte("szcSCmn6aBnU68z5mXmqAtpZWXs6V7KUiY/mJXOaMYU="), // Generate a secure key
		csrf.Secure(true), // Use 'false' in development only, should be 'true' in production with HTTPS
	)

	metaInfo := Meta{
		Author:      config.ApplicationAuthor,
		Version:     config.ApplicationVersion,
		Application: config.ApplicationName,
		Owner:       config.ApplicationOwner,
	}

	mux.Use(httprate.Limit(
		10000000,       // requests
		10*time.Second, // per duration
		httprate.WithKeyFuncs(httprate.KeyByIP, httprate.KeyByEndpoint, httprate.KeyByRealIP),
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			//http.Error(w, "", http.StatusTooManyRequests)

			jsonResponse := JSONResponse{
				Error:   true,
				Message: "too many requests",
				Data:    nil,
			}

			err := app.writeJSON(w, http.StatusTooManyRequests, jsonResponse)
			if err != nil {
				return
			}
		}),
	))

	mux.NotFound(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse := JSONResponse{
			Error:   true,
			Message: "404 page not found",
			Data: map[string]interface{}{
				"route":  r.URL.Path,
				"method": r.Method,
				"meta":   metaInfo,
			},
		}

		err := app.writeJSON(w, http.StatusNotFound, jsonResponse)
		if err != nil {
			return
		}
	})

	mux.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse := JSONResponse{
			Error:   true,
			Message: "method not allowed",
			Data:    nil,
		}

		err := app.writeJSON(w, http.StatusMethodNotAllowed, jsonResponse)
		if err != nil {
			return
		}
	})

	// Serve static files from the "css" directory
	mux.Handle("/css/*", http.StripPrefix("/css/", http.FileServer(http.Dir("css"))))

	// Serve static files from the "img" directory
	mux.Handle("/images/*", http.StripPrefix("/images/", http.FileServer(http.Dir("images"))))

	// Serve static files from the "js" directory
	mux.Handle("/js/*", http.StripPrefix("/js/", http.FileServer(http.Dir("js"))))

	mux.Get("/", app.home)

	// Route untuk membaca file dari SeaweedFS
	mux.HandleFunc("/read", app.handleReadFile)

	// Route untuk mengunggah file ke SeaweedFS
	mux.HandleFunc("/upload", app.handleUploadFile)

	mux.Get("/panduan-monitor-loket-1", app.panduan1)
	mux.Get("/panduan-monitor-loket-2", app.panduan2)

	mux.Get("/riwayat-aktivitas", app.userActivities)

	// Apply CSRF middleware to the entire router
	mux.Group(func(r chi.Router) {
		r.Use(csrfMiddleware) // Enable CSRF protection for this group
		r.Use(app.autoLogoutMiddleware)

		// Authentication and session management
		r.Get("/login", app.login)
		r.Post("/auth/login", app.loginAction)
		r.Get("/auth/logout", app.logout)

		// Dashboard
		r.Get("/dashboard", app.dashboard)

		// Manajemen Arsip dan User
		r.Get("/permohonan", app.permohonan)
		r.Get("/user", app.manajemenUser)

		// Arsip CRUD
		r.Get("/permohonan/edit/{id}", app.editPermohonan)
		r.Post("/permohonan/edit/{id}", app.editPermohonan)
		r.Post("/permohonan/create", app.createPermohonan)
		r.Get("/permohonan/create", app.createPermohonan)
		r.Get("/permohonan/delete/{id}", app.deletePermohonan)

		// User CRUD
		r.Get("/user/edit/{id}", app.editUser)  // Untuk form edit user
		r.Post("/user/edit/{id}", app.editUser) // Untuk submit perubahan
		r.Get("/user/create", app.createUser)   // Add user page
		r.Post("/user/create", app.createUser)
		r.Get("/user/delete/{id}", app.deleteUser)

		r.Get("/profile", app.editProfile)
		r.Post("/profile", app.editProfile)

		r.Get("/dashboard/arsip-changes", app.getArsipChanges)

		r.Get("/monitoring-dan-pelaporan", app.monitoringPelaporan)
		r.Get("/monitoring-dan-pelaporan/unduh", app.downloadMonitoringPelaporan)

		r.Post("/update-last-activity", app.updateLastActivity)
		r.Get("/dashboard/online-users", app.getOnlineUsers)

		r.Get("/dashboard/inventory-progress", app.getInventoryProgress)
		r.Get("/dashboard/inventory-progress-trends", app.getInventoryProgressTrends)

	})
	mux.Get("/generate-api-key", app.generateAPIKey)
	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("/static/wa-meta/a", func(w http.ResponseWriter, r *http.Request) {
		rnd.HTMLString(w, http.StatusOK, `
<h1>Hello</h1>`)
	})

	mux.HandleFunc("/static/wa-meta/toc", func(w http.ResponseWriter, r *http.Request) {
		rnd.HTMLString(w, http.StatusOK, `
<h1>Hello</h1>`)
	})

	return mux
}

func (app *application) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := log.Logger.With().
			Str("request_id", uuid.New().String()).
			Str("url", r.URL.String()).
			Str("method", r.Method).
			Logger()
		ctx := log.WithContext(r.Context())

		if r.URL.Path == "/" ||
			r.URL.Path == "/dashboard/online-users" || r.URL.Path == "/update-last-activity" ||
			r.URL.Path == "/images/office.jpg" || r.URL.Path == "/riwayat-aktivitas" ||
			r.URL.Path == "/panduan-monitor-loket-1" || r.URL.Path == "/panduan-monitor-loket-2" ||
			r.URL.Path == "/images/favicon.ico" || r.URL.Path == "/login" || r.URL.Path == "/auth/login" ||
			r.URL.Path == "/auth/logout" || r.URL.Path == "/profile" ||
			r.URL.Path == "/images/logo.png" || r.URL.Path == "/images/favicon-96x96.png" ||
			r.URL.Path == "/images/favicon.svg" || r.URL.Path == "/images/favicon.ico" ||
			r.URL.Path == "/images/apple-touch-icon.png" || r.URL.Path == "/images/site.webmanifest" ||
			r.URL.Path == "/images/web-app-manifest-192x192.png" || r.URL.Path == "/images/web-app-manifest-512x512.png" {

			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Get the session
		session, _ := app.Store.Get(r, config.SessionName)

		// Check if user is authenticated
		if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Ambil data user dari database beserta permissions
		userData, err := app.DB.GetUserByID(session.Values["user.id"].(string))
		if err != nil {
			log.Error().Err(err).Msg("Failed to fetch user data")
			//http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
			return
		}

		permissions, ok := userData["permissions"].([]map[string]interface{})
		if !ok {
			log.Error().Msg("Invalid permissions format")
			//http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
			return
		}

		// Cek apakah user memiliki permission untuk slug dan method
		requestSlug := r.URL.Path
		requestMethod := strings.ToLower(r.Method)

		hasPermission := false
		for _, perm := range permissions {

			//log.Info().Msgf("Perm: %v", perm)

			permSlug, ok := perm["slug"].(string)
			permMethod, okMethod := perm["method"].(string)
			if !ok || !okMethod {
				continue
			}

			// Periksa apakah request slug diawali dengan permission slug
			if strings.HasPrefix(requestSlug, permSlug) && strings.ToLower(permMethod) == requestMethod {
				hasPermission = true
				break
			}
		}

		if !hasPermission {
			log.Warn().Msgf("Permission denied for %s %s", requestMethod, requestSlug)
			//http.Error(w, "Forbidden", http.StatusForbidden)
			_ = app.writeJSON(w, http.StatusForbidden, JSONResponse{
				Error:   true,
				Message: "anda tidak diizinkan mengakses halaman ini",
				Data:    nil,
			})
			return
		}

		// Log akses yang berhasil
		log.Info().Msgf("Access granted for %s %s", requestMethod, requestSlug)

		// Teruskan ke handler berikutnya
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (app *application) prometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		totalRequests.Inc()

		// Create a custom response writer to intercept status code
		recorder := &statusRecorder{w, http.StatusOK}
		defer func() {
			status := recorder.status
			statusText := http.StatusText(status)
			responseStatus.WithLabelValues(statusText).Inc()
		}()

		// Serve the request using the custom response writer
		next.ServeHTTP(recorder, r)

		// Record request duration
		duration := time.Since(start).Seconds()
		httpDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration)

		// Record active connections
		activeConnections.Inc()
		defer activeConnections.Dec()

		// Record request size
		requestSize.Observe(float64(r.ContentLength))
	})
}

// statusRecorder is a custom response writer to intercept status code
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(status int) {
	sr.status = status
	sr.ResponseWriter.WriteHeader(status)
}

func (app *application) autoLogoutMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ambil sesi pengguna
		session, _ := app.Store.Get(r, config.SessionName)
		auth, ok := session.Values["authenticated"].(bool)

		// Jika pengguna tidak diautentikasi, lanjutkan tanpa memeriksa Redis
		if !ok || !auth {
			next.ServeHTTP(w, r)
			return
		}

		userID, ok := session.Values["user.id"].(string)
		if !ok || userID == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Cek waktu terakhir aktivitas pengguna dari Redis
		lastActivityKey := fmt.Sprintf("user:%s:last_activity", userID)
		client := app.redis

		lastActivity, err := client.Get(context.Background(), lastActivityKey).Result()
		if errors.Is(err, redis.Nil) || lastActivity == "" {
			// Tidak ada aktivitas atau waktu aktivitas habis
			log.Warn().Msgf("User %s session expired due to inactivity", userID)
			app.logout(w, r)
			return
		} else if err != nil {
			log.Error().Err(err).Msg("Error checking last activity in Redis")
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Perbarui waktu terakhir aktivitas ke Redis
		err = client.Set(context.Background(), lastActivityKey, time.Now().Unix(), 30*time.Minute).Err()
		if err != nil {
			log.Error().Err(err).Msg("Error updating last activity in Redis")
		}

		// Lanjutkan ke handler berikutnya
		next.ServeHTTP(w, r)
	})
}
