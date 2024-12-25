package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/gorilla/sessions"
	_ "github.com/jackc/pgx/v4/stdlib" // Import driver PostgreSQL
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"net/url"
	"strconv"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"monitor-loket/config"
	redisClient "monitor-loket/internal/redis"
	"monitor-loket/internal/repository"
	"monitor-loket/internal/repository/dbrepo"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type application struct {
	DSN                   string
	redisClient           redisClient.Client
	redis                 *redis.Client
	Host                  string
	DB                    repository.DatabaseRepo
	RedisDSN              string
	Store                 *sessions.CookieStore
	SeaweedFSFilerBaseURL string
}

var (
	totalRequests     = prometheus.NewCounter(prometheus.CounterOpts{Name: "total_requests", Help: "Total number of HTTP requests"})
	responseStatus    = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "response_status", Help: "HTTP response status count"}, []string{"status"})
	httpDuration      = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "http_duration_seconds", Help: "Duration of HTTP requests in seconds", Buckets: prometheus.DefBuckets}, []string{"method", "path"})
	activeConnections = prometheus.NewGauge(prometheus.GaugeOpts{Name: "active_connections", Help: "Number of active connections"})
	requestSize       = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "http_request_size_bytes", Help: "Size of HTTP requests in bytes", Buckets: []float64{100, 200, 500, 1000, 2000, 5000}})
	responseSize      = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "http_response_size_bytes", Help: "Size of HTTP responses in bytes", Buckets: []float64{100, 200, 500, 1000, 2000, 5000}})
)

func init() {
	prometheus.MustRegister(totalRequests, responseStatus, httpDuration, activeConnections, requestSize, responseSize)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := application{}

	setupLogging()
	if err := loadEnvironmentVariables(); err != nil {
		log.Fatal().Err(err).Msg("Error loading .env file")
	}

	app.initializeApp()

	defer app.cleanup()

	h2s := &http2.Server{}
	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", config.ApplicationPort),
		Handler:      h2c.NewHandler(app.routes(), h2s),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Info().Msgf("Listening monitor-loket [h2c] on %s", server.Addr)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("Failed to start server")
		}
	}()

	<-ctx.Done()

	log.Info().Msg("Shutting down gracefully...")
	if err := server.Shutdown(context.Background()); err != nil {
		log.Error().Err(err).Msg("Failed to shutdown HTTP server")
	}
}

func (app *application) initializeApp() {
	app.DSN = os.Getenv("DSN")
	app.Host = os.Getenv("HOST")
	app.RedisDSN = os.Getenv("REDIS_DSN")
	app.Store = sessions.NewCookieStore([]byte(os.Getenv("SESSION_KEY")))
	app.SeaweedFSFilerBaseURL = os.Getenv("SEAWEEED_FS_FILER_BASE_URL")

	app.connectToDB()
	// Assign client to app.redis

	// Connect to Redis
	app.connectToRedis()
}

func setupLogging() {
	logFile, err := os.OpenFile("app.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to open log file")
	}
	log.Logger = zerolog.New(zerolog.MultiLevelWriter(os.Stdout, logFile)).With().Timestamp().Logger()
}

func loadEnvironmentVariables() error {
	return godotenv.Load()
}

func (app *application) connectToDB() {
	db, err := sql.Open("pgx", app.DSN) // Gunakan "pgx" sebagai nama driver
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to connect to the database")
	}

	app.DB = &dbrepo.PostgresDBRepo{DB: db}
	log.Info().Msg("Connected to the database")
}

func (app *application) connectToRedis() {
	// Parse Redis DSN
	host, port, password, db, err := parseRedisDSN(app.RedisDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse Redis DSN")
	}

	// Configure Redis options
	options := &redis.Options{
		Addr:     fmt.Sprintf("%s:%s", host, port),
		Password: password, // Empty string means no password
		DB:       db,
	}

	// Create Redis client
	client := redis.NewClient(options)

	// Ping Redis to ensure connection is established
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Ping(ctx).Result(); err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to Redis")
	}

	// Log successful connection
	log.Info().Msg("Connected to Redis successfully")

	// Assign client to app.redis
	app.redis = client
}

func (app *application) cleanup() {
	if app.DB != nil {
		if err := app.DB.Connection().Close(); err != nil {
			log.Error().Err(err).Msg("Error closing database connection")
		} else {
			log.Info().Msg("Database connection closed")
		}
	}

	if app.redis != nil {
		if err := app.redis.Close(); err != nil {
			log.Error().Err(err).Msg("Error closing Redis connection")
		} else {
			log.Info().Msg("Redis connection closed")
		}
	}
}

func parseRedisDSN(dsn string) (host string, port string, password string, db int, err error) {
	redisURL, err := url.Parse(dsn)
	if err != nil {
		return "", "", "", 0, err
	}

	if redisURL.Scheme != "redis" {
		return "", "", "", 0, errors.New("invalid scheme for Redis DSN")
	}

	password, _ = redisURL.User.Password()
	host = redisURL.Hostname()
	port = redisURL.Port()
	if port == "" {
		port = "6379" // Default Redis port
	}

	dbStr := redisURL.Path
	if len(dbStr) > 1 {
		dbStr = dbStr[1:] // Remove leading slash
		db, err = strconv.Atoi(dbStr)
		if err != nil {
			return "", "", "", 0, errors.New("invalid database number")
		}
	} else {
		db = 0 // Default database
	}

	return host, port, password, db, nil
}
