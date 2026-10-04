package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config DB
type DbConfig struct {
	DBHost   string
	DBPort   string
	DBName   string
	DBUser   string
	DBPass   string
	AuthWord string
}

type qrRequest struct {
	Code string `json:"code"`
}

type Asset struct {
	Code      string `json:"code"`
	ScanCount int64  `json:"scanCount"`
	CreatedAt string `json:"createdAt"`
	LastSeen  string `json:"lastSeen"`
}

func loadDBConfig() DbConfig {
	get := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	return DbConfig{
		DBHost:   get("DB_HOST", "localhost"),
		DBPort:   get("DB_PORT", "5432"),
		DBName:   get("DB_NAME", "autoMagazijn"),
		DBUser:   get("DB_USER", "admin"),
		DBPass:   get("DB_PASS", "pass"),
		AuthWord: get("AUTH_WORD", "GLR"),
	}
}

func (c DbConfig) connString() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		c.DBHost, c.DBPort, c.DBUser, c.DBPass, c.DBName,
	)
}

type app struct {
	pool *pgxpool.Pool
	cfg  DbConfig
}

func main() {
	a, err := setupApp()
	if err != nil {
		log.Fatalf("startup failed: %v", err)
	}
	listenerServer(a)
}

func setupApp() (*app, error) {
	cfg := loadDBConfig()

	pool, err := pgxpool.New(context.Background(), cfg.connString())
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// Auto-create tables if they don't exist (safe to run every time)
	if err := setupSchema(context.Background(), pool); err != nil {
		return nil, fmt.Errorf("setup schema: %w", err)
	}

	return &app{pool: pool, cfg: cfg}, nil
}

// setupSchema creates tables automatically at startup
func setupSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS assets (
			id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			code       TEXT UNIQUE NOT NULL,
			scan_count BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			last_seen  TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE TABLE IF NOT EXISTS scan_events (
			id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			asset_code TEXT NOT NULL REFERENCES assets(code),
			scanned_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`)
	return err
}

func (a *app) authenticate() bool {
	return false // placeholder
}

func (a *app) qrCodeHandler(w http.ResponseWriter, r *http.Request) {
	var req qrRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	fmt.Printf("--- INCOMING SCAN ---\nCode: %s\n----------\n", req.Code)

	// validate the code format before touching the DB
	if !strings.HasPrefix(req.Code, "ICT-ACC-") || len(req.Code) != 16 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "invalid code format",
		})
		return
	}

	// upsert: creates the asset if new, bumps counter if existing
	var (
		isNew    bool
		count    int64
		lastSeen time.Time
	)
	err := a.pool.QueryRow(r.Context(), `
		INSERT INTO assets (code)
		VALUES ($1)
		ON CONFLICT (code) DO UPDATE
		SET scan_count = assets.scan_count + 1,
		    last_seen  = now()
		RETURNING (created_at = last_seen) AS is_new, scan_count, last_seen
	`, req.Code).Scan(&isNew, &count, &lastSeen)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "database error",
		})
		return
	}

	// log the scan event for history
	_, _ = a.pool.Exec(r.Context(),
		`INSERT INTO scan_events (asset_code) VALUES ($1)`, req.Code)

	status := "existing asset"
	if isNew {
		status = "registered new asset"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"code":      req.Code,
		"new":       isNew,
		"status":    status,
		"scanCount": count,
		"lastSeen":  lastSeen.Format(time.RFC3339),
	})
}

func (a *app) listAssetsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := a.pool.Query(r.Context(), `
		SELECT code, scan_count, created_at, last_seen
		FROM assets
		ORDER BY last_seen DESC
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "database error",
		})
		return
	}
	defer rows.Close()

	assets := []Asset{}
	for rows.Next() {
		var (
			code      string
			scanCount int64
			createdAt time.Time
			lastSeen  time.Time
		)
		if err := rows.Scan(&code, &scanCount, &createdAt, &lastSeen); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "database error",
			})
			return
		}
		assets = append(assets, Asset{
			Code:      code,
			ScanCount: scanCount,
			CreatedAt: createdAt.Format(time.RFC3339),
			LastSeen:  lastSeen.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, assets)
}

func (a *app) deleteAllAssetsHandler(w http.ResponseWriter, r *http.Request) {
	// delete scan events first, then assets (avoids FK violation if no cascade)
	tag, err := a.pool.Exec(r.Context(), `
		DELETE FROM scan_events;
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "database error",
		})
		return
	}
	eventsDeleted := tag.RowsAffected()

	tag, err = a.pool.Exec(r.Context(), `
		DELETE FROM assets;
	`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "database error",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":       true,
		"assetsDeleted": tag.RowsAffected(),
		"eventsDeleted": eventsDeleted,
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Auth-Word")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func listenerServer(a *app) {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/items", a.deleteAllAssetsHandler)
	mux.HandleFunc("GET /api/v1/items", a.listAssetsHandler)
	mux.HandleFunc("/api/v1/scan", a.qrCodeHandler)
	mux.Handle("/", http.FileServer(http.Dir("./app/frontend")))

	fmt.Println("Listening on :5173")
	log.Fatal(http.ListenAndServe(":5173", corsMiddleware(mux)))
}
