package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Masterminds/sprig"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/csrf"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
	"github.com/xuri/excelize/v2"
	"html/template"
	"io"
	"mime/multipart"
	"monitor-loket/config"
	"monitor-loket/internal/models"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Meta struct {
	Application string `json:"application"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Owner       string `json:"owner"`
	AppURL      string `json:"application_url"`
}

func (app *application) home(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	_ = app.writeJSON(w, http.StatusOK, JSONResponse{
		Meta: Meta{
			Application: config.ApplicationName,
			Version:     config.ApplicationVersion,
			Author:      config.ApplicationAuthor,
			Owner:       config.ApplicationOwner,
			AppURL:      fmt.Sprintf("%s/%s", app.Host, "login"),
		},
	})
}

func (app *application) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	log := log.Ctx(ctx).With().Str("func", "handler").Logger()
	log.Debug().Msg("processing request")

	// Get the session
	session, _ := app.Store.Get(r, config.SessionName)

	// Check if pengguna is authenticated redirect to dashboard
	if auth, ok := session.Values["authenticated"].(bool); ok && auth {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	app.renderTemplate(w, "templates/login.html", PageData{
		Data: map[string]interface{}{
			config.KeyBaseURL:            app.Host,
			config.KeyApplicationName:    config.ApplicationName,
			config.KeyApplicationVersion: config.ApplicationVersion,
			config.KeyApplicationOwner:   config.ApplicationOwner,
			config.KeyApplicationAuthor:  config.ApplicationAuthor,
			"csrfField":                  csrf.TemplateField(r),
		},
	})
	return
}

func (app *application) proxySeaweedFSFile(w http.ResponseWriter, r *http.Request) {

	var requestPayloadFile RequestPayloadFile

	err := app.readJSON(w, r, &requestPayloadFile)
	if err != nil {
		log.Debug().Msg("Error parsing request payload")
		_ = app.errorJSON(w, err)
		return
	}

	// Get from seaweed
	seaweedFilerURL := fmt.Sprintf("%s/%s", app.SeaweedFSFilerBaseURL, requestPayloadFile.FilePath)
	log.Debug().Msgf("SeaweedFS Filer URL: %s", seaweedFilerURL)
	resp, err := http.Get(seaweedFilerURL)
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	log.Debug().Msgf("Response status code: %d", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		_ = app.errorJSON(w, errors.New("file not found"), http.StatusNotFound)
		return
	}

	//Set headers for the file response (MIME type, etc.)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", requestPayloadFile.FilePath))
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))

	// Proxy the content from SeaweedFS directly to the client
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		//http.Error(w, "Failed to serve file", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
	}
}

// handler untuk membaca file dari SeaweedFS
func (app *application) handleReadFile(w http.ResponseWriter, r *http.Request) {
	// Get the session
	session, _ := app.Store.Get(r, config.SessionName)

	// Check if pengguna is authenticated
	if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	var requestPayloadFile RequestPayloadFile
	err := app.readJSON(w, r, &requestPayloadFile)
	if err != nil {
		log.Debug().Msgf("Error parsing request payload: %v", err)
		_ = app.errorJSON(w, err)
		return
	}

	// Validasi filePath
	if requestPayloadFile.FilePath == "" {
		err = fmt.Errorf("file path is empty")
		log.Debug().Msgf("Error: %v", err)
		_ = app.errorJSON(w, err)
		return
	}

	resp, err := app.readFromSeaweedFS(requestPayloadFile.FilePath)
	if err != nil {
		log.Debug().Msgf("Error reading file from SeaweedFS: %v", err)
		_ = app.errorJSON(w, err)
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Debug().Msgf("Error closing response body: %v", err)
		}
	}()

	// Atur header untuk file download
	fileName := path.Base(requestPayloadFile.FilePath) // Ambil nama file dari path
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))

	// Salin konten file ke respons
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Debug().Msgf("Error copying file content: %v", err)
		_ = app.errorJSON(w, err)
	}
}

func (app *application) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	file, header, err := r.FormFile("file")
	if err != nil {
		_ = app.errorJSON(w, errors.New("file parameter is missing"))
		return
	}
	defer func(file multipart.File) {
		_ = file.Close()
	}(file)

	// Validasi filePath
	filePath := r.FormValue("filePath")
	if filePath == "" {
		_ = app.errorJSON(w, errors.New("filePath parameter is missing"))
		return
	}

	// Ambil ukuran file langsung dari header.Size tanpa perlu mengecek error
	fileSize := header.Size

	resp, err := app.uploadToSeaweedFS(filePath, file, fileSize)
	if err != nil {
		log.Debug().Msgf("Error uploading file to SeaweedFS: %v", err)
		_ = app.errorJSON(w, err)
		return
	}

	log.Debug().Msgf("Response status code: %d", resp.StatusCode)

	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		_ = app.errorJSON(w, errors.New("failed to upload file"))
		return
	}

	_ = app.writeJSON(w, http.StatusCreated, map[string]string{"message": "File uploaded successfully"})
}

func (app *application) loginAction(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		log.Debug().Msgf("Error parsing form: %v", err)
		_ = app.writeJSON(w, http.StatusBadRequest, JSONResponse{
			Error:   true,
			Message: "Error parsing form",
			Data:    nil,
		})
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	// periksa jika email dan password kosong
	if email == "" || password == "" {
		// Render login form with error message
		tmpl := template.New("login.html").Funcs(sprig.FuncMap())
		tmpl, err := tmpl.ParseFiles("templates/login.html")
		if err != nil {
			//http.Error(w, "Failed to load login template", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)

			return
		}

		data := PageData{
			Data: map[string]interface{}{
				config.KeyApplicationName:    config.ApplicationName,
				config.KeyApplicationVersion: config.ApplicationVersion,
				config.KeyApplicationOwner:   config.ApplicationOwner,
				config.KeyApplicationAuthor:  config.ApplicationAuthor,
				"csrfField":                  csrf.TemplateField(r),
				"ErrorMessage":               "Email dan password tidak boleh kosong!",
			},
		}

		err = tmpl.Execute(w, data)
		if err != nil {
			//http.Error(w, "Failed to render login template", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
		}
		return
	}

	// Default log activity data for LOGIN_FAILED
	activity := map[string]interface{}{
		"user_id":     config.SystemUserID,
		"table_name":  "public.users",
		"record_id":   nil,
		"action":      "LOGIN_FAILED",
		"description": fmt.Sprintf("Percobaan login gagal dengan email: %s", email),
		"changes": map[string]interface{}{
			"after": map[string]interface{}{
				"username": email,
				"password": password,
			},
			"before": nil,
		},
	}

	isUserActive, err := app.DB.LoginCheckUserIsActive(email)
	if err != nil {
		log.Debug().Msgf("Error checking pengguna active: %v", err)
		_ = app.writeJSON(w, http.StatusInternalServerError, JSONResponse{
			Error:   true,
			Message: "Error checking pengguna active",
			Data:    nil,
		})
		return
	}

	if !isUserActive {
		// Log aktivitas LOGIN_FAILED
		_ = app.DB.LogActivity(activity)

		// Render login form with error message
		tmpl := template.New("login.html").Funcs(sprig.FuncMap())
		tmpl, err := tmpl.ParseFiles("templates/login.html")
		if err != nil {
			//http.Error(w, "Failed to load login template", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)

			return
		}

		data := PageData{
			Data: map[string]interface{}{
				config.KeyApplicationName:    config.ApplicationName,
				config.KeyApplicationVersion: config.ApplicationVersion,
				config.KeyApplicationOwner:   config.ApplicationOwner,
				config.KeyApplicationAuthor:  config.ApplicationAuthor,
				"csrfField":                  csrf.TemplateField(r),
				"ErrorMessage":               "Akun Anda tidak aktif atau tidak tersedia, periksa kembali email dan password Anda.",
			},
		}

		err = tmpl.Execute(w, data)
		if err != nil {
			//http.Error(w, "Failed to render login template", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
		}
		return
	}

	// Check if the email and password are correct
	user, err := app.DB.Login(email, password)
	if err != nil {
		// Log aktivitas LOGIN_FAILED
		_ = app.DB.LogActivity(activity)

		// Render login form with error message
		tmpl := template.New("login.html").Funcs(sprig.FuncMap())
		tmpl, err := tmpl.ParseFiles("templates/login.html")
		if err != nil {
			//http.Error(w, "Failed to load login template", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
			return
		}

		data := PageData{
			Data: map[string]interface{}{
				config.KeyApplicationName:    config.ApplicationName,
				config.KeyApplicationVersion: config.ApplicationVersion,
				config.KeyApplicationOwner:   config.ApplicationOwner,
				config.KeyApplicationAuthor:  config.ApplicationAuthor,
				"csrfField":                  csrf.TemplateField(r),
				"ErrorMessage":               "Email atau password yang Anda masukkan salah.",
			},
		}

		err = tmpl.Execute(w, data)
		if err != nil {
			//http.Error(w, "Failed to render login template", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
		}
		return
	}

	// Set session data
	session, _ := app.Store.Get(r, config.SessionName)
	session.Values["authenticated"] = true
	session.Values["user.id"] = user["id"]
	session.Values["user.email"] = user["email"]
	session.Values["user.role"] = user["role"]
	session.Values["user.is_active"] = user["is_active"]
	session.Values["user.jabatan"] = user["jabatan"]
	session.Values["user.nama"] = user["nama"]
	session.Values["user.nip"] = user["nip"]
	session.Values["user.phone"] = user["phone"]

	err = session.Save(r, w)
	if err != nil {
		log.Debug().Msgf("Error saving session: %v", err)
		return
	}

	// Log aktivitas LOGIN_SUCCESS
	activity["user_id"] = user["id"]
	activity["table_name"] = "public.users"
	activity["record_id"] = user["id"]
	activity["action"] = "LOGIN_SUCCESS"
	activity["description"] = fmt.Sprintf("Pengguna %s berhasil logged in", email)
	activity["changes"] = map[string]interface{}{
		"after": map[string]interface{}{
			"username":  email,
			"password":  password,
			"role":      user["role"],
			"nama":      user["nama"],
			"nip":       user["nip"],
			"phone":     user["phone"],
			"is_active": user["is_active"],
			"jabatan":   user["jabatan"],
		},
		"before": nil,
	}

	err = app.DB.LogActivity(activity)
	if err != nil {
		log.Err(err).Msg("Failed to log login activity")
	}

	client := app.redis
	lastActivityKey := fmt.Sprintf("user:%s:last_activity", user["id"])
	err = client.Set(context.Background(), lastActivityKey, time.Now().Unix(), 1*time.Minute).Err()
	if err != nil {
		log.Error().Err(err).Msg("Error setting last activity in Redis")
	}

	if user["role"] == config.RoleSuperadmin {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	} else if user["role"] == config.RoleAdmin {
		http.Redirect(w, r, "/permohonan", http.StatusSeeOther)
		return
	} else {
		http.Redirect(w, r, "/user", http.StatusSeeOther)
		return
	}
}

func (app *application) dashboard(w http.ResponseWriter, r *http.Request) {
	// Get the session
	session, _ := app.Store.Get(r, config.SessionName)

	// Check if pengguna is authenticated
	if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	startDate := r.URL.Query().Get("start_date")
	endDate := r.URL.Query().Get("end_date")
	//filter := r.URL.Query().Get("filter")

	// Data untuk setiap komponen
	totalActivities, err := app.DB.CountActivities()
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}

	recentActivities, err := app.DB.GetRecentActivities(5) // Batasi ke 5 aktivitas terakhir
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}

	activitiesByTable, err := app.DB.CountActivitiesByTable()
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}

	filteredActivities, err := app.DB.GetFilteredActivities(startDate, endDate)
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}

	// Ambil data berdasarkan filter
	//permohonanChanges, err := app.DB.GetFilteredPermohonanChanges(filter)
	//if err != nil {
	//	http.Error(w, "Failed to fetch permohonan changes: "+err.Error(), http.StatusInternalServerError)
	//	return
	//}

	csrfToken := csrf.Token(r) // Ambil token CSRF sebagai string

	app.renderTemplate(w, "templates/dashboard.html", PageData{
		Data: map[string]interface{}{
			config.KeyBaseURL:             app.Host,
			config.KeyApplicationName:     config.ApplicationName,
			config.KeyApplicationVersion:  config.ApplicationVersion,
			config.KeyApplicationOwner:    config.ApplicationOwner,
			config.KeyApplicationAuthor:   config.ApplicationAuthor,
			config.KeySessionAdminEmail:   session.Values["user.email"],
			config.KeySessionAdminNama:    session.Values["user.nama"],
			config.KeySessionAdminRole:    session.Values["user.role"],
			config.KeySessionAdminID:      session.Values["user.id"],
			config.KeySessionAdminPhone:   session.Values["user.phone"],
			config.KeySessionAdminJabatan: session.Values["user.jabatan"],
			"totalActivities":             totalActivities,
			"recentActivities":            recentActivities,
			"activitiesByTable":           activitiesByTable,
			"filteredActivities":          filteredActivities,
			"startDate":                   startDate,
			"endDate":                     endDate,
			"csrfToken":                   csrfToken,
			//"permohonanChanges":                permohonanChanges,
		},
	})
	return
}

func (app *application) deletePermohonan(w http.ResponseWriter, r *http.Request) {
	// Ambil session
	session, err := app.Store.Get(r, config.SessionName)
	if err != nil {
		//http.Error(w, "Failed to get session", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	// Periksa autentikasi pengguna
	auth, ok := session.Values["authenticated"].(bool)
	if !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Ambil ID permohonan dari URL
	permohonanID := chi.URLParam(r, "id")
	if permohonanID == "" {
		//http.Error(w, "Arsip ID is required", http.StatusBadRequest)
		_ = app.errorJSON(w, errors.New("id permohonan diperlukan"))
		return
	}

	// Ambil data permohonan sebelum penghapusan untuk log aktivitas
	permohonanBefore, err := app.DB.GetPermohonanByID(permohonanID)
	if err != nil {
		log.Err(err).Msg("Failed to fetch permohonan data for logging")
		//http.Error(w, "Failed to fetch permohonan data", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	if permohonanBefore == nil {
		//http.Error(w, "Arsip not found", http.StatusNotFound)
		_ = app.errorJSON(w, errors.New("permohonan tidak ditemukan"))
		return
	}

	// Lakukan penghapusan permohonan
	err = app.DB.HardDeletePermohonan(permohonanID)
	if err != nil {
		log.Err(err).Msg("Failed to delete permohonan")
		//http.Error(w, "Failed to delete permohonan", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	// Catat aktivitas penghapusan
	userID := session.Values["user.id"].(string)
	activity := map[string]interface{}{
		"user_id":     userID,
		"table_name":  "app.permohonan",
		"record_id":   permohonanID,
		"action":      "DELETE",
		"description": fmt.Sprintf("Arsip dengan ID %s berhasil dihapus oleh pengguna %s", permohonanID, session.Values["user.email"]),
		"changes": map[string]interface{}{
			"before": permohonanBefore,
			"after":  nil, // Karena permohonan sudah dihapus
		},
	}

	err = app.DB.LogActivity(activity)
	if err != nil {
		log.Err(err).Msg("Failed to log activity for delete")
		//http.Error(w, "Failed to log activity", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	// Redirect ke halaman permohonan dengan notifikasi sukses
	http.Redirect(w, r, "/permohonan?delete=1", http.StatusSeeOther)
}

func (app *application) permohonan(w http.ResponseWriter, r *http.Request) {
	// Ambil parameter query string untuk notifikasi dan filter
	success := r.URL.Query().Get("success")
	del := r.URL.Query().Get("delete")
	search := r.URL.Query().Get("search")
	sort := r.URL.Query().Get("sort")
	order := r.URL.Query().Get("order")

	var (
		permohonan   []map[string]interface{}
		totalRecords int
		err          error
	)

	// Ambil parameter pagination, default ke halaman 1 dan 10 item per halaman
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	perPage, err := strconv.Atoi(r.URL.Query().Get("per_page"))
	if err != nil || perPage < 1 {
		perPage = 10
	}
	if search == "" {
		search = "" // Inisialisasi dengan string kosong
	}

	// Ambil session pengguna
	session, err := app.Store.Get(r, config.SessionName)
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}

	// Cek apakah pengguna sudah login
	auth, ok := session.Values["authenticated"].(bool)
	if !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Ambil data permohonan berdasarkan filter, pencarian, dan pagination

	userRole := session.Values["user.role"]
	userID := session.Values["user.id"].(string)

	// Ambil data permohonan berdasarkan peran pengguna
	if userRole == config.RoleSuperadmin {
		permohonan, totalRecords, err = app.DB.GetAllPermohonan(page, perPage, sort, order, search)
	} else if userRole == config.RoleAdmin {
		permohonan, totalRecords, err = app.DB.GetAllPermohonanByUserID(page, perPage, sort, order, search, userID)
	}

	if err != nil {
		log.Debug().Msgf("Error fetching permohonan data: %v", err)
		_ = app.errorJSON(w, err)
		return
	}

	// Hitung detail pagination
	totalPages := (totalRecords + perPage - 1) / perPage
	hasPrevPage := page > 1
	hasNextPage := page < totalPages
	prevPage := page - 1
	nextPage := page + 1

	// Hitung range halaman untuk navigasi pagination
	pageRange := 5
	startPage := max(1, page-(pageRange/2))
	endPage := min(totalPages, startPage+pageRange-1)

	if endPage-startPage < pageRange-1 {
		startPage = max(1, endPage-pageRange+1)
	}

	// Bangun daftar halaman dengan ellipses
	var pages []int
	if startPage > 1 {
		pages = append(pages, 1)
		if startPage > 2 {
			pages = append(pages, -1) // Ellipsis
		}
	}
	for i := startPage; i <= endPage; i++ {
		pages = append(pages, i)
	}
	if endPage < totalPages {
		if endPage < totalPages-1 {
			pages = append(pages, -1) // Ellipsis
		}
		pages = append(pages, totalPages)
	}

	// Ambil token CSRF untuk keamanan
	csrfToken := csrf.Token(r)

	// Render template dengan data yang telah dikumpulkan
	app.renderTemplate(w, "templates/permohonan.html", PageData{
		Data: map[string]interface{}{
			config.KeyBaseURL:             app.Host,
			config.KeyApplicationName:     config.ApplicationName,
			config.KeyApplicationVersion:  config.ApplicationVersion,
			config.KeyApplicationOwner:    config.ApplicationOwner,
			config.KeyApplicationAuthor:   config.ApplicationAuthor,
			config.KeySessionAdminEmail:   session.Values["user.email"],
			config.KeySessionAdminNama:    session.Values["user.nama"],
			config.KeySessionAdminRole:    session.Values["user.role"],
			config.KeySessionAdminID:      session.Values["user.id"],
			config.KeySessionAdminPhone:   session.Values["user.phone"],
			config.KeySessionAdminJabatan: session.Values["user.jabatan"],
			config.KeyListPermohonan:      permohonan,
			"current_page":                page,
			"items_per_page":              perPage,
			"has_prev_page":               hasPrevPage,
			"has_next_page":               hasNextPage,
			"prev_page":                   prevPage,
			"next_page":                   nextPage,
			"pages":                       pages,
			"search":                      search,
			"total_records":               totalRecords,
			"total_pages":                 totalPages,
			"csrfField":                   csrf.TemplateField(r),
			"QuerySuccess":                success,
			"QueryDelete":                 del,
			"csrfToken":                   csrfToken,
		},
	})
}

func (app *application) createPermohonan(w http.ResponseWriter, r *http.Request) {
	// Ambil session
	session, err := app.Store.Get(r, config.SessionName)
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}

	// Periksa autentikasi pengguna
	auth, ok := session.Values["authenticated"].(bool)
	if !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Tangani request GET
	if r.Method == http.MethodGet {
		tmpl := template.New("create-permohonan.html").Funcs(sprig.FuncMap())
		tmpl, err = tmpl.ParseFiles("templates/create-permohonan.html")
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}

		data := PageData{
			Data: map[string]interface{}{
				config.KeyBaseURL:             app.Host,
				config.KeyApplicationName:     config.ApplicationName,
				config.KeyApplicationVersion:  config.ApplicationVersion,
				config.KeyApplicationOwner:    config.ApplicationOwner,
				config.KeyApplicationAuthor:   config.ApplicationAuthor,
				config.KeySessionAdminEmail:   session.Values["user.email"],
				config.KeySessionAdminNama:    session.Values["user.nama"],
				config.KeySessionAdminRole:    session.Values["user.role"],
				config.KeySessionAdminID:      session.Values["user.id"],
				config.KeySessionAdminPhone:   session.Values["user.phone"],
				config.KeySessionAdminJabatan: session.Values["user.jabatan"],
				"csrfField":                   csrf.TemplateField(r),
			},
		}
		err = tmpl.Execute(w, data)
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}
		return
	}

	// Tangani request POST
	if r.Method == http.MethodPost {
		err := r.ParseForm()
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}

		// Ambil input form
		record := map[string]interface{}{
			"dikuasakan":           r.FormValue("dikuasakan") == "on", // Checkbox
			"nama_kuasa":           r.FormValue("nama_kuasa"),
			"nomor_berkas":         r.FormValue("nomor_berkas"),
			"phone":                r.FormValue("phone"),
			"nama_pemohon":         r.FormValue("nama_pemohon"),
			"jenis_permohonan":     r.FormValue("jenis_permohonan"),
			"ppat":                 r.FormValue("ppat"),
			"nama_penyerah_berkas": r.FormValue("nama_penyerah_berkas"),
			"nomor_hak":            r.FormValue("nomor_hak"),
			"jenis_hak":            r.FormValue("jenis_hak"),
			"kecamatan":            r.FormValue("kecamatan"),
			"kelurahan":            r.FormValue("kelurahan"),
			//"created_by":       fmt.Sprintf("%s (%s)", session.Values["user.email"], session.Values["user.nama"]),
			"created_by": session.Values["user.id"].(string),
		}

		// Gunakan repository untuk menyimpan data
		ids, err := app.DB.CreatePermohonan([]map[string]interface{}{record})
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}

		record["id"] = ids[0]

		// Hapus key yang tidak diperlukan
		keysToRemove := []string{
			"gorilla.csrf.Token",
			"created_by_nama",
			"updated_by_nama",
		}

		for _, key := range keysToRemove {
			delete(record, key)
		}

		// Pencatatan log aktivitas
		activity := map[string]interface{}{
			"user_id":     session.Values["user.id"].(string),
			"table_name":  "app.permohonan",
			"action":      "CREATE",
			"description": fmt.Sprintf("Permohonan baru ditambahkan oleh pengguna %s", session.Values["user.email"]),
			"changes": map[string]interface{}{
				"before": nil,
				"after":  record,
			},
		}

		err = app.DB.LogActivity(activity)
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}

		// Redirect ke daftar permohonan dengan notifikasi sukses
		http.Redirect(w, r, "/permohonan?success=1", http.StatusSeeOther)
	}

	// Jika method tidak didukung
	_ = app.errorJSON(w, errors.New("method not allowed"))
}

func (app *application) editPermohonan(w http.ResponseWriter, r *http.Request) {
	// Ambil session
	session, err := app.Store.Get(r, config.SessionName)
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}

	// Periksa autentikasi pengguna
	auth, ok := session.Values["authenticated"].(bool)
	if !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Ambil UUID dari URL
	permohonanID := chi.URLParam(r, "id")
	if permohonanID == "" {
		_ = app.errorJSON(w, errors.New("id permohonan diperlukan"))
		return
	}

	// Tangani request GET
	if r.Method == http.MethodGet {
		success := r.URL.Query().Get("success")
		permohonan, err := app.DB.GetPermohonanByID(permohonanID)
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}
		if permohonan == nil {
			_ = app.errorJSON(w, errors.New("permohonan tidak ditemukan"))
			return
		}

		tmpl := template.New("edit-permohonan.html").Funcs(sprig.FuncMap())
		tmpl, err = tmpl.ParseFiles("templates/edit-permohonan.html")
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}

		data := PageData{
			Data: map[string]interface{}{
				config.KeyBaseURL:             app.Host,
				config.KeyApplicationName:     config.ApplicationName,
				config.KeyApplicationVersion:  config.ApplicationVersion,
				config.KeyApplicationOwner:    config.ApplicationOwner,
				config.KeyApplicationAuthor:   config.ApplicationAuthor,
				config.KeySessionAdminEmail:   session.Values["user.email"],
				config.KeySessionAdminNama:    session.Values["user.nama"],
				config.KeySessionAdminRole:    session.Values["user.role"],
				config.KeySessionAdminID:      session.Values["user.id"],
				config.KeySessionAdminPhone:   session.Values["user.phone"],
				config.KeySessionAdminJabatan: session.Values["user.jabatan"],
				config.KeyPermohonan:          permohonan,
				"id":                          permohonanID,
				"csrfField":                   csrf.TemplateField(r),
				"QuerySuccess":                success,
			},
		}
		err = tmpl.Execute(w, data)
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}
		return
	}

	// Tangani request POST
	if r.Method == http.MethodPost {

		// Ambil data permohonan sebelumnya
		before, err := app.DB.GetPermohonanByID(permohonanID)
		if err != nil || before == nil {
			_ = app.errorJSON(w, err)
			return
		}

		// Ambil data dari form
		permohonanData := map[string]interface{}{}
		for key, values := range r.Form {
			permohonanData[key] = values[0]
		}

		// Parsing boolean untuk `dikuasakan`
		permohonanData["dikuasakan"] = r.FormValue("dikuasakan") == "true"

		log.Debug().Msgf("Permohonan data: %v", r.FormValue("dikuasakan"))

		// Tambahkan `updated_by` dan waktu update
		userID := session.Values["user.id"].(string)
		//permohonanData["updated_by"] = fmt.Sprintf("%s (%s)", session.Values["user.email"], session.Values["user.nama"])
		permohonanData["updated_by"] = userID
		permohonanData["updated_at"] = time.Now().Format(time.RFC3339)

		log.Debug().Msgf("Permohonan data: %v", permohonanData)

		// Perbarui data di database
		err = app.DB.UpdatePermohonanByID(permohonanID, permohonanData)
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}

		// Hapus key yang tidak diperlukan
		keysToRemove := []string{
			"gorilla.csrf.Token",
			"created_by_nama",
			"updated_by_nama",
		}

		for _, key := range keysToRemove {
			delete(permohonanData, key)
		}

		// Catat aktivitas
		activity := map[string]interface{}{
			"user_id":     userID,
			"table_name":  "app.permohonan",
			"record_id":   permohonanID,
			"action":      "UPDATE",
			"description": fmt.Sprintf("Permohonan %s diperbarui oleh pengguna %s", permohonanID, session.Values["user.email"]),
			"changes": map[string]interface{}{
				"before": before,
				"after":  permohonanData,
			},
		}

		err = app.DB.LogActivity(activity)
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}

		// Redirect ke halaman edit
		http.Redirect(w, r, fmt.Sprintf("/permohonan/edit/%s?success=1", permohonanID), http.StatusSeeOther)
		return
	}

	// Jika method tidak didukung
	_ = app.errorJSON(w, errors.New("method tidak diizinkan"))
}

func uploadFileToSeaweedFS(file multipart.File, originalFilename, endpoint string) (string, error) {
	// Buat KSUID sebagai prefix
	uniqueID := ksuid.New().String()

	// Gabungkan KSUID dengan nama file asli
	sanitizedFilename := strings.ReplaceAll(originalFilename, " ", "_") // Ganti spasi dengan underscore
	newFilename := fmt.Sprintf("%s-%s", uniqueID, sanitizedFilename)

	// Validasi tipe file
	allowedContentTypes := []string{"image/jpeg", "image/jpg", "image/png", "application/pdf"}

	// Gunakan bufio.NewReader untuk membaca header file
	fileHeader := make([]byte, 512)
	_, err := file.Read(fileHeader)
	if err != nil {
		log.Err(err).Msg("Failed to read file header for validation")
		return "", err
	}

	// Reset file reader ke awal setelah membaca header
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		log.Err(err).Msg("Failed to reset file reader after validation")
		return "", err
	}

	// Tentukan tipe konten file
	contentType := http.DetectContentType(fileHeader)
	log.Debug().Msgf("Detected content type: %s", contentType)

	// Periksa apakah tipe konten diizinkan
	isAllowed := false
	for _, allowedType := range allowedContentTypes {
		if contentType == allowedType {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		log.Error().Msgf("File type %s is not allowed", contentType)
		return "", fmt.Errorf("file type %s is not allowed", contentType)
	}

	// Siapkan request body
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Tambahkan file ke dalam multipart form
	part, err := writer.CreateFormFile("file", newFilename)
	if err != nil {
		log.Err(err).Msg("Failed to create form file")
		return "", err
	}
	_, err = io.Copy(part, file)
	if err != nil {
		log.Err(err).Msg("Failed to copy file to form")
		return "", err
	}

	// Tutup writer multipart
	err = writer.Close()
	if err != nil {
		log.Err(err).Msg("Failed to close multipart writer")
		return "", err
	}

	// Buat permintaan POST
	req, err := http.NewRequest("POST", endpoint, body)
	if err != nil {
		log.Err(err).Msg("Failed to create request")
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Kirim permintaan
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Err(err).Msg("Failed to send request")
		return "", err
	}
	defer resp.Body.Close()

	// Log respons server
	responseBody, _ := io.ReadAll(resp.Body)
	log.Debug().Msgf("Server response: %s", string(responseBody))

	// Periksa status respons
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		log.Error().Msgf("Failed to upload file, status: %d", resp.StatusCode)
		return "", fmt.Errorf("failed to upload file, status: %d", resp.StatusCode)
	}

	// Bangun URL file
	fileURL := newFilename
	log.Debug().Msgf("File uploaded successfully: %s", fileURL)

	return fileURL, nil
}

func sanitizeString(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func (app *application) logout(w http.ResponseWriter, r *http.Request) {
	// Ambil sesi pengguna
	session, _ := app.Store.Get(r, config.SessionName)
	userID, ok := session.Values["user.id"].(string)

	if ok && userID != "" {
		client := app.redis
		lastActivityKey := fmt.Sprintf("user:%s:last_activity", userID)

		// Hapus data Redis untuk aktivitas terakhir
		if err := client.Del(context.Background(), lastActivityKey).Err(); err != nil {
			log.Error().Err(err).Msg("Error deleting last activity in Redis during logout")
		}

		// Log aktivitas logout
		activity := map[string]interface{}{
			"user_id":     userID,
			"table_name":  "public.users",
			"record_id":   userID,
			"action":      "LOGOUT",
			"description": fmt.Sprintf("Pengguna %s logged out", session.Values["user.email"]),
			"changes": map[string]interface{}{
				"before": nil,
				"after":  nil,
			},
		}

		err := app.DB.LogActivity(activity)
		if err != nil {
			log.Error().Err(err).Msg("Failed to log logout activity")
		}
	}

	// Hapus semua data sesi
	session.Options.MaxAge = -1 // Hapus sesi dengan mengatur usia menjadi negatif
	err := session.Save(r, w)
	if err != nil {
		log.Error().Err(err).Msg("Failed to save session during logout")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Redirect ke halaman login
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (app *application) generateAPIKey(w http.ResponseWriter, r *http.Request) {

	log.Debug().Msg("Generating API key")

	k, err := generateAPIKey(128)

	if err != nil {
		_ = app.writeJSON(w, http.StatusInternalServerError, JSONResponse{
			Error:   true,
			Message: "error generating API key",
			Data:    nil,
		})
		return
	}

	// Send a response
	_ = app.writeJSON(w, http.StatusOK, JSONResponse{
		Message: "api key generated",
		Error:   false,
		Data:    k,
	})
}

func (app *application) manajemenUser(w http.ResponseWriter, r *http.Request) {
	success := r.URL.Query().Get("success")
	delete := r.URL.Query().Get("delete")

	// Get the session
	session, err := app.Store.Get(r, config.SessionName)
	if err != nil {
		//http.Error(w, "Failed to get session", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	// Check if pengguna is authenticated
	auth, ok := session.Values["authenticated"].(bool)
	if !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Get pagination parameters from query string
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1 // Default page
	}

	perPage, err := strconv.Atoi(r.URL.Query().Get("per_page"))
	if err != nil || perPage < 1 {
		perPage = 5 // Default items per page
	}

	// Optional sorting and search
	sort := r.URL.Query().Get("sort")
	order := r.URL.Query().Get("order")
	search := r.URL.Query().Get("search")

	// Validate session values and role
	userRole, ok := session.Values["user.role"].(string)
	if !ok {
		log.Debug().Msg("Invalid pengguna role in session")
		_ = app.errorJSON(w, fmt.Errorf("invalid pengguna role in session"))
		return
	}

	// Variables to store results
	var (
		users []map[string]interface{}
		total int
		err1  error
	)

	if userRole == config.RoleSuperadmin {
		// Get all users for "Superadmin"
		users, total, err1 = app.DB.GetAllUsers(page, perPage, sort, order, search)
	} else {
		// Get users excluding "Superadmin"
		users, total, err1 = app.DB.GetAllUsersExceptKakan(page, perPage, sort, order, search)
	}

	if err1 != nil {
		log.Debug().Msgf("Failed to get users: %v", err)
		_ = app.errorJSON(w, err)
		return
	}

	// Calculate pagination details
	totalPages := (total + perPage - 1) / perPage // Total number of pages
	hasPrevPage := page > 1
	hasNextPage := page < totalPages
	prevPage := page - 1
	nextPage := page + 1

	// Generate page range for pagination
	pageRange := 5
	startPage := max(1, page-(pageRange/2))
	endPage := min(totalPages, startPage+pageRange-1)

	if endPage-startPage < pageRange-1 {
		startPage = max(1, endPage-pageRange+1)
	}

	// Add ellipses handling for pagination
	pages := []int{}
	if startPage > 1 {
		pages = append(pages, 1) // Always show the first page
		if startPage > 2 {
			pages = append(pages, -1) // Ellipsis
		}
	}

	for i := startPage; i <= endPage; i++ {
		pages = append(pages, i)
	}

	if endPage < totalPages {
		if endPage < totalPages-1 {
			pages = append(pages, -1) // Ellipsis
		}
		pages = append(pages, totalPages) // Always show the last page
	}

	// Load precompiled templates or parse them
	tmpl := template.New("manajemen-user.html").Funcs(sprig.FuncMap())
	tmpl, err = tmpl.ParseFiles("templates/manajemen-user.html")
	if err != nil {
		log.Debug().Msgf("Failed to load template: %v", err)
		_ = app.errorJSON(w, err)
		return
	}

	csrfToken := csrf.Token(r) // Ambil token CSRF sebagai string
	// Prepare the data for the template
	data := PageData{
		Data: map[string]interface{}{
			config.KeyBaseURL:             app.Host,
			config.KeyApplicationName:     config.ApplicationName,
			config.KeyApplicationVersion:  config.ApplicationVersion,
			config.KeyApplicationOwner:    config.ApplicationOwner,
			config.KeyApplicationAuthor:   config.ApplicationAuthor,
			config.KeySessionAdminEmail:   session.Values["user.email"],
			config.KeySessionAdminNama:    session.Values["user.nama"],
			config.KeySessionAdminRole:    session.Values["user.role"],
			config.KeySessionAdminID:      session.Values["user.id"],
			config.KeySessionAdminPhone:   session.Values["user.phone"],
			config.KeySessionAdminJabatan: session.Values["user.jabatan"],
			config.KeyListUser:            users, // List of pengguna data
			"current_page":                page,
			"items_per_page":              perPage,
			"has_prev_page":               hasPrevPage,
			"has_next_page":               hasNextPage,
			"prev_page":                   prevPage,
			"next_page":                   nextPage,
			"pages":                       pages, // List of pages with ellipses
			"search":                      search,
			"total_records":               total,
			"total_pages":                 totalPages,
			"QuerySuccess":                success, // Add query parameter success
			"QueryDelete":                 delete,  // Add query parameter delete
			"csrfToken":                   csrfToken,
		},
	}

	// Render the template
	err = tmpl.Execute(w, data)
	if err != nil {
		log.Debug().Msgf("Failed to render template: %v", err)
		_ = app.errorJSON(w, err)
		return
	}
}

func (app *application) editUser(w http.ResponseWriter, r *http.Request) {
	// Ambil session
	session, err := app.Store.Get(r, config.SessionName)
	if err != nil {
		//http.Error(w, "Failed to get session", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	// Parse pengguna ID from URL
	userID := chi.URLParam(r, "id")
	if userID == "" {
		//http.Error(w, "Invalid pengguna ID", http.StatusBadRequest)
		_ = app.errorJSON(w, errors.New("id pengguna tidak valid"))
		return
	}

	if r.Method == http.MethodGet {

		// Ambil query parameter "success"
		success := r.URL.Query().Get("success")

		// GET request: Render edit pengguna form
		user, err := app.DB.GetUserByID(userID)
		if err != nil {
			log.Debug().Msgf("Failed to fetch user: %v", err)
			//http.Error(w, "User not found", http.StatusNotFound)
			_ = app.errorJSON(w, err)
			return
		}

		// Fetch all permissions and mark selected ones
		permissions, err := app.DB.GetAllPermissionsWithSelection(user["permissions"].([]map[string]interface{}))
		if err != nil {
			log.Debug().Msgf("Failed to fetch permissions: %v", err)
			//http.Error(w, "Failed to load permissions", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
			return
		}

		//log.Debug().Msgf("Fetched permissions: %+v", permissions)

		// Load template
		tmpl := template.New("edit-user.html").Funcs(sprig.FuncMap())
		tmpl, err = tmpl.ParseFiles("templates/edit-user.html")
		if err != nil {
			log.Debug().Msgf("Failed to load template: %v", err)
			_ = app.errorJSON(w, err)
			return
		}

		// Prepare data for template
		data := PageData{
			Data: map[string]interface{}{
				config.KeyBaseURL:             app.Host,
				config.KeyApplicationName:     config.ApplicationName,
				config.KeyApplicationVersion:  config.ApplicationVersion,
				config.KeyApplicationOwner:    config.ApplicationOwner,
				config.KeyApplicationAuthor:   config.ApplicationAuthor,
				config.KeySessionAdminEmail:   session.Values["user.email"],
				config.KeySessionAdminNama:    session.Values["user.nama"],
				config.KeySessionAdminRole:    session.Values["user.role"],
				config.KeySessionAdminID:      session.Values["user.id"],
				config.KeySessionAdminPhone:   session.Values["user.phone"],
				config.KeySessionAdminJabatan: session.Values["user.jabatan"],
				config.KeyUser:                user, // Pass pengguna data to template
				config.KeyListPermision:       permissions,
				"csrfField":                   csrf.TemplateField(r),
				"QuerySuccess":                success, // Add query parameter success
			},
		}

		// Render the template
		if err = tmpl.Execute(w, data); err != nil {
			log.Debug().Msgf("Failed to render template: %v", err)
			_ = app.errorJSON(w, err)
		}
		return
	}

	if r.Method == http.MethodPost {

		// Ambil data pengguna sebelumnya
		before, err := app.DB.GetUserByID(userID)
		if err != nil || before == nil {
			//http.Error(w, "Failed to fetch pengguna data", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
			return
		}

		// POST request: Handle form submission
		if err := r.ParseForm(); err != nil {
			//http.Error(w, "Invalid form data", http.StatusBadRequest)
			_ = app.errorJSON(w, err)
			return
		}

		isActive := r.FormValue("is_active") == "true"

		// Ambil password dan konfirmasi password
		password := r.FormValue("password")
		confirmPassword := r.FormValue("confirm_password")

		if password != "" && password != confirmPassword {
			//http.Error(w, "Password dan Konfirmasi Password tidak sesuai", http.StatusBadRequest)
			_ = app.errorJSON(w, errors.New("password dan konfirmasi tidak sesuai"))
			return
		}

		//log.Debug().Msgf("isActive: %v", isActive)
		// Collect form data
		data := map[string]interface{}{
			"nama": r.FormValue("nama"),
			//"email":       r.FormValue("email"),
			"phone":   r.FormValue("phone"),
			"nip":     r.FormValue("nip"),
			"jabatan": r.FormValue("jabatan"),
			//"role":        r.FormValue("role"),
			"is_active":   isActive,
			"password":    password, // Tambahkan password jika ada
			"permissions": parsePermissions(r.Form["permissions[]"]),
		}

		//log.Debug().Msgf("User data: %+v", data)

		// Update pengguna in the database
		err = app.DB.UpdateUser(userID, data)
		if err != nil {
			log.Debug().Msgf("Failed to update user: %v", err)
			//http.Error(w, "Failed to update user", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
			return
		}

		// Pencatatan log aktivitas
		activity := map[string]interface{}{
			"user_id":     session.Values["user.id"],
			"table_name":  "public.users",
			"record_id":   userID,
			"action":      "UPDATE",
			"description": fmt.Sprintf("Pengguna %s diperbarui oleh pengguna %s", userID, session.Values["user.email"]),
			"changes": map[string]interface{}{
				"before": before,
				"after":  data,
			},
		}

		err = app.DB.LogActivity(activity)
		if err != nil {
			log.Err(err).Msg("Failed to log activity")
			//http.Error(w, "Failed to log activity", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
			return
		}

		// Redirect to pengguna management page with success message
		http.Redirect(w, r, fmt.Sprintf("/user/edit/%s?success=1", userID), http.StatusSeeOther)
		return
	}

	// Return 405 Method Not Allowed for unsupported methods
	//http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	_ = app.errorJSON(w, errors.New("method tidak diizinkan"))
}

// Helper function to parse permissions from form values
func parsePermissions(permissionIDs []string) []int {
	var parsed []int
	for _, id := range permissionIDs {
		permID, err := strconv.Atoi(id)
		if err == nil {
			parsed = append(parsed, permID)
		}
	}
	return parsed
}

func (app *application) createUser(w http.ResponseWriter, r *http.Request) {
	// Ambil session
	session, err := app.Store.Get(r, config.SessionName)
	if err != nil {
		//http.Error(w, "Failed to get session", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	// Periksa autentikasi pengguna
	auth, ok := session.Values["authenticated"].(bool)
	if !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		// Fetch all permissions for the dropdown
		permissions, err := app.DB.GetAllPermissions()
		if err != nil {
			log.Debug().Msgf("Failed to fetch permissions: %v", err)
			//http.Error(w, "Failed to load permissions", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
			return
		}

		// Load template
		tmpl := template.New("create-user.html").Funcs(sprig.FuncMap())
		tmpl, err = tmpl.ParseFiles("templates/create-user.html")
		if err != nil {
			log.Debug().Msgf("Failed to load template: %v", err)
			_ = app.errorJSON(w, err)
			return
		}

		data := PageData{
			Data: map[string]interface{}{
				config.KeyBaseURL:             app.Host,
				config.KeyApplicationName:     config.ApplicationName,
				config.KeyApplicationVersion:  config.ApplicationVersion,
				config.KeyApplicationOwner:    config.ApplicationOwner,
				config.KeyApplicationAuthor:   config.ApplicationAuthor,
				config.KeySessionAdminEmail:   session.Values["user.email"],
				config.KeySessionAdminNama:    session.Values["user.nama"],
				config.KeySessionAdminRole:    session.Values["user.role"],
				config.KeySessionAdminID:      session.Values["user.id"],
				config.KeySessionAdminPhone:   session.Values["user.phone"],
				config.KeySessionAdminJabatan: session.Values["user.jabatan"],
				config.KeyListPermision:       permissions,
				"csrfField":                   csrf.TemplateField(r),
			},
		}

		if err = tmpl.Execute(w, data); err != nil {
			log.Debug().Msgf("Failed to render template: %v", err)
			_ = app.errorJSON(w, err)
		}
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			//http.Error(w, "Invalid form data", http.StatusBadRequest)
			_ = app.errorJSON(w, err)
			return
		}

		// Password validation
		password := r.FormValue("password")
		confirmPassword := r.FormValue("confirm_password")
		if password != confirmPassword {
			//http.Error(w, "Password dan Konfirmasi Password tidak cocok", http.StatusBadRequest)
			_ = app.errorJSON(w, errors.New("password dan konfirmasi tidak sesuai"))
			return
		}

		// Parsing permissions
		permissions := parsePermissions(r.Form["permissions[]"])
		if permissions == nil {
			permissions = []int{} // Set default empty permissions if none selected
		}

		// Collect form data
		isActive := r.FormValue("is_active") == "true"
		data := map[string]interface{}{
			"nama":    r.FormValue("nama"),
			"email":   r.FormValue("email"),
			"phone":   r.FormValue("phone"),
			"nip":     r.FormValue("nip"),
			"jabatan": r.FormValue("jabatan"),
			//"role":        r.FormValue("role"),
			"role":        config.RoleAdmin,
			"password":    password, // Add hashed password
			"is_active":   isActive,
			"permissions": permissions,
		}

		//log.Debug().Msgf("User data: %+v", data)

		// Insert pengguna into the database
		err = app.DB.CreateUser(data)
		if err != nil {
			log.Debug().Msgf("Failed to create user: %v", err)
			//http.Error(w, "Failed to create user", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
			return
		}

		// Pencatatan log aktivitas
		activity := map[string]interface{}{
			"user_id":     session.Values["user.id"],
			"table_name":  "public.users",
			"record_id":   nil,
			"action":      "CREATE",
			"description": fmt.Sprintf("Pengguna %s dibuat oleh pengguna %s", data["email"], session.Values["user.email"]),
			"changes": map[string]interface{}{
				"before": nil,
				"after":  data,
			},
		}

		err = app.DB.LogActivity(activity)
		if err != nil {
			log.Err(err).Msg("Failed to log activity")
			//http.Error(w, "Failed to log activity", http.StatusInternalServerError)
			_ = app.errorJSON(w, err)
			return
		}

		// Redirect to pengguna management page
		http.Redirect(w, r, "/user?success=1", http.StatusSeeOther)
		return
	}

	// Method not allowed
	//http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	_ = app.errorJSON(w, errors.New("method tidak diizinkan"))
}

func (app *application) deleteUser(w http.ResponseWriter, r *http.Request) {
	// Ambil session
	session, err := app.Store.Get(r, config.SessionName)
	if err != nil {
		//http.Error(w, "Failed to get session", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	// Periksa autentikasi pengguna
	auth, ok := session.Values["authenticated"].(bool)
	if !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Ambil ID pengguna dari URL
	userID := chi.URLParam(r, "id")
	if userID == "" {
		//http.Error(w, "User ID is required", http.StatusBadRequest)
		_ = app.errorJSON(w, errors.New("id pengguna diperlukan"))
		return
	}

	// Ambil data pengguna sebelum penghapusan untuk log aktivitas
	userBefore, err := app.DB.GetUserByID(userID)
	if err != nil {
		log.Err(err).Msg("Failed to fetch pengguna data for logging")
		//http.Error(w, "Failed to fetch pengguna data", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	if userBefore == nil {
		//http.Error(w, "User not found", http.StatusNotFound)
		_ = app.errorJSON(w, errors.New("user tidak ditemukan"))
		return
	}

	// Lakukan penghapusan user
	err = app.DB.HardDeleteUser(userID)
	if err != nil {
		log.Err(err).Msg("Failed to delete user")
		//http.Error(w, "Failed to delete user", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	// Catat aktivitas penghapusan
	userIDSession := session.Values["user.id"].(string)
	activity := map[string]interface{}{
		"user_id":     userIDSession,
		"table_name":  "public.users",
		"record_id":   userID,
		"action":      "DELETE",
		"description": fmt.Sprintf("Pengguna dengan ID %s berhasil dihapus oleh pengguna %s", userID, session.Values["user.email"]),
		"changes": map[string]interface{}{
			"before": userBefore,
			"after":  nil, // Karena pengguna sudah dihapus
		},
	}

	err = app.DB.LogActivity(activity)
	if err != nil {
		log.Err(err).Msg("Failed to log activity for delete")
		//http.Error(w, "Failed to log activity", http.StatusInternalServerError)
		_ = app.errorJSON(w, err)
		return
	}

	// Redirect ke halaman manajemen pengguna dengan notifikasi sukses
	http.Redirect(w, r, "/user?delete=1", http.StatusSeeOther)
}

func (app *application) editProfile(w http.ResponseWriter, r *http.Request) {
	// Ambil session
	session, err := app.Store.Get(r, config.SessionName)
	if err != nil {
		http.Error(w, "Failed to get session", http.StatusInternalServerError)
		return
	}

	// Ambil pengguna ID dari session
	userID, ok := session.Values["user.id"].(string)
	if !ok || userID == "" {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}

	if r.Method == http.MethodGet {
		success := r.URL.Query().Get("success")
		// Ambil data user
		user, err := app.DB.GetUserByID(userID)
		if err != nil {
			log.Debug().Msgf("Failed to fetch user: %v", err)
			http.Error(w, "Failed to load profile data", http.StatusInternalServerError)
			return
		}

		// Ambil hak akses pengguna
		permissions, err := app.DB.GetUserPermissions(userID)
		if err != nil {
			log.Debug().Msgf("Failed to fetch pengguna permissions: %v", err)
			http.Error(w, "Failed to fetch permissions", http.StatusInternalServerError)
			return
		}

		user["permissions"] = permissions // Tambahkan ke data user

		// Render template
		tmpl := template.New("profile.html")
		tmpl, err = tmpl.ParseFiles("templates/profile.html")
		if err != nil {
			log.Debug().Msgf("Failed to load template: %v", err)
			_ = app.errorJSON(w, err)
			return
		}

		data := PageData{
			Data: map[string]interface{}{
				config.KeyBaseURL:             app.Host,
				config.KeyApplicationName:     config.ApplicationName,
				config.KeyApplicationVersion:  config.ApplicationVersion,
				config.KeyApplicationOwner:    config.ApplicationOwner,
				config.KeyApplicationAuthor:   config.ApplicationAuthor,
				config.KeySessionAdminEmail:   session.Values["user.email"],
				config.KeySessionAdminNama:    session.Values["user.nama"],
				config.KeySessionAdminRole:    session.Values["user.role"],
				config.KeySessionAdminID:      session.Values["user.id"],
				config.KeySessionAdminPhone:   session.Values["user.phone"],
				config.KeySessionAdminJabatan: session.Values["user.jabatan"],
				config.KeyUser:                user, // Pass pengguna data to template
				"csrfField":                   csrf.TemplateField(r),
				"QuerySuccess":                success, // Add query parameter success
			},
		}

		if err = tmpl.Execute(w, data); err != nil {
			log.Debug().Msgf("Failed to render template: %v", err)
			_ = app.errorJSON(w, err)
		}
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			_ = app.errorJSON(w, err)
			return
		}

		// Password validation
		password := r.FormValue("password")
		confirmPassword := r.FormValue("confirm_password")

		// Validasi jika password tidak kosong
		if password != "" {
			if password != confirmPassword {
				_ = app.errorJSON(w, errors.New("password dan konfirmasi tidak sesuai"))
				return
			}

			// Cek panjang minimal password
			if len(password) < 7 {
				_ = app.errorJSON(w, errors.New("password harus minimal 7 karakter"))
				return
			}

			// Validasi angka, huruf, dan karakter spesial
			hasLetter := false
			hasDigit := false
			hasSpecial := false
			for _, c := range password {
				switch {
				case unicode.IsLetter(c):
					hasLetter = true
				case unicode.IsDigit(c):
					hasDigit = true
				case unicode.IsPunct(c) || unicode.IsSymbol(c):
					hasSpecial = true
				}
			}

			if !hasLetter || !hasDigit || !hasSpecial {
				_ = app.errorJSON(w, errors.New("password harus mengandung huruf, angka, dan karakter spesial"))
				return
			}
		}

		// Data yang akan diperbarui
		data := map[string]interface{}{
			"nama":     r.FormValue("nama"),
			"phone":    r.FormValue("phone"),
			"nip":      r.FormValue("nip"),
			"jabatan":  r.FormValue("jabatan"),
			"password": password, // Kosong jika tidak diperbarui
		}

		// Update pengguna di database
		err := app.DB.UpdateUserProfile(userID, data)
		if err != nil {
			log.Debug().Msgf("Failed to update profile: %v", err)
			_ = app.errorJSON(w, err)
			return
		}

		// Redirect dengan pesan sukses
		http.Redirect(w, r, "/profile?success=1", http.StatusSeeOther)
	}
}

func (app *application) getArsipChanges(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")

	changes, err := app.DB.GetFilteredPermohonanChanges(filter)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch permohonan changes: %v", err), http.StatusInternalServerError)
		return
	}

	// Jika `changes` nil, pastikan JSON yang dikembalikan adalah array kosong
	if changes == nil {
		changes = []models.PermohonanChange{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(changes)
}

func (app *application) monitoringPelaporan(w http.ResponseWriter, r *http.Request) {
	// Get the session
	session, _ := app.Store.Get(r, config.SessionName)

	// Check if pengguna is authenticated
	if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Ambil filter dari query string
	filter := r.URL.Query().Get("filter")
	limit := r.URL.Query().Get("limit") // Batasi jumlah record

	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		limitInt = 1000 // Default limit
	}

	if limitInt > 1000 {
		limitInt = 1000 // Batasi ke 1000 record
	}

	// Ambil data permohonan dengan filter
	records, err := app.DB.GetFilteredPermohonanRecords(filter, limitInt) // Batasi ke 100 record
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}

	// convert int to string
	l := strconv.Itoa(limitInt)

	csrfToken := csrf.Token(r) // Ambil token CSRF sebagai string

	// Render template monitoring dan pelaporan
	app.renderTemplate(w, "templates/monitoring-pelaporan.html", PageData{
		Data: map[string]interface{}{
			config.KeyBaseURL:             app.Host,
			config.KeyApplicationName:     config.ApplicationName,
			config.KeyApplicationVersion:  config.ApplicationVersion,
			config.KeyApplicationOwner:    config.ApplicationOwner,
			config.KeyApplicationAuthor:   config.ApplicationAuthor,
			config.KeySessionAdminEmail:   session.Values["user.email"],
			config.KeySessionAdminNama:    session.Values["user.nama"],
			config.KeySessionAdminRole:    session.Values["user.role"],
			config.KeySessionAdminID:      session.Values["user.id"],
			config.KeySessionAdminPhone:   session.Values["user.phone"],
			config.KeySessionAdminJabatan: session.Values["user.jabatan"],
			"Filter":                      filter,
			config.KeyListPermohonan:      records,
			"Limit":                       l,
			"csrfToken":                   csrfToken,
		},
	})
	return
}

func (app *application) downloadMonitoringPelaporan(w http.ResponseWriter, r *http.Request) {
	// Get the session
	session, _ := app.Store.Get(r, config.SessionName)

	// Check if pengguna is authenticated
	if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Ambil filter dan format dari query string
	filter := r.URL.Query().Get("filter")
	format := r.URL.Query().Get("format") // "pdf" atau "excel"
	limit := r.URL.Query().Get("limit")   // Batasi jumlah record

	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		limitInt = 1000 // Tidak ada batasan
	}

	if limitInt > 1000 {
		limitInt = 1000 // Batasi ke 1000 record
	}

	// Ambil data arsip dengan filter
	records, err := app.DB.GetFilteredPermohonanRecords(filter, limitInt) // Batasi ke 1000 record
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}

	if format == "excel" {
		// Generate Excel
		excelFile, err := app.generateExcel(records)
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}

		// Set header response untuk file Excel
		w.Header().Set("Content-Disposition", "attachment; filename=monitoring_pelaporan.xlsx")
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")

		// Kirim file Excel ke client
		if err := excelFile.Write(w); err != nil {
			_ = app.errorJSON(w, err)
			return
		}

	} else {
		// Generate PDF
		pdf := app.generatePDF(records)

		// Set header response untuk file PDF
		w.Header().Set("Content-Disposition", "attachment; filename=monitoring_pelaporan.pdf")
		w.Header().Set("Content-Type", "application/pdf")

		// Kirim file PDF ke client
		err = pdf.Output(w)
		if err != nil {
			_ = app.errorJSON(w, err)
			return
		}
	}
}

func (app *application) generateExcel(records []models.PermohonanRecord) (*excelize.File, error) {
	// Buat file Excel baru
	f := excelize.NewFile()
	sheetName := "MonitoringPelaporan"
	f.SetSheetName("Sheet1", sheetName)

	// Header Tabel
	headers := []string{
		"Dikuasakan", "Nama Kuasa", "Nomor Berkas", "Telepon", "Nama Pemohon", "Jenis Permohonan",
		"PPAT", "Nama Penyerah Berkas", "Jenis Hak", "Nomor Hak", "Kecamatan", "Kelurahan",
		"Dibuat Tanggal", "Dibuat Oleh", "Diperbarui Tanggal", "Diperbarui Oleh",
	}

	// Tambahkan Header ke Excel
	for i, header := range headers {
		col := excelColumnName(i+1) + "1" // Konversi indeks ke nama kolom Excel
		f.SetCellValue(sheetName, col, header)
	}

	// Isi Data Tabel
	for idx, record := range records {
		row := strconv.Itoa(idx + 2) // Baris dimulai dari baris kedua setelah header

		// Isi data sesuai kolom
		f.SetCellValue(sheetName, excelColumnName(1)+row, boolToYesNo(record.Dikuasakan))
		f.SetCellValue(sheetName, excelColumnName(2)+row, record.NamaKuasa)
		f.SetCellValue(sheetName, excelColumnName(3)+row, record.NomorBerkas)
		f.SetCellValue(sheetName, excelColumnName(4)+row, record.Phone)
		f.SetCellValue(sheetName, excelColumnName(5)+row, record.NamaPemohon)
		f.SetCellValue(sheetName, excelColumnName(6)+row, record.JenisPermohonan)
		f.SetCellValue(sheetName, excelColumnName(7)+row, record.PPAT)
		f.SetCellValue(sheetName, excelColumnName(8)+row, record.NamaPenyerahBerkas)
		f.SetCellValue(sheetName, excelColumnName(9)+row, record.JenisHak)
		f.SetCellValue(sheetName, excelColumnName(10)+row, record.NomorHak)
		f.SetCellValue(sheetName, excelColumnName(11)+row, record.Kecamatan)
		f.SetCellValue(sheetName, excelColumnName(12)+row, record.Kelurahan)
		f.SetCellValue(sheetName, excelColumnName(13)+row, record.CreatedAt)
		f.SetCellValue(sheetName, excelColumnName(14)+row, record.CreatedByNama)
		f.SetCellValue(sheetName, excelColumnName(15)+row, record.UpdatedAt)
		f.SetCellValue(sheetName, excelColumnName(16)+row, record.UpdatedByNama)
	}

	// Atur kolom agar auto-fit
	for i := 0; i < len(headers); i++ {
		col := excelColumnName(i + 1)
		_ = f.SetColWidth(sheetName, col, col, 20) // Set lebar kolom default 20
	}

	return f, nil
}

// excelColumnName mengonversi indeks kolom menjadi nama kolom Excel
func excelColumnName(n int) string {
	if n <= 0 {
		return ""
	}
	col := ""
	for n > 0 {
		n--
		col = string('A'+(n%26)) + col
		n /= 26
	}
	return col
}

// boolToYesNo mengonversi nilai boolean menjadi string "Ya" atau "Tidak"
func boolToYesNo(val bool) string {
	if val {
		return "Ya"
	}
	return "Tidak"
}

func (app *application) panduan1(w http.ResponseWriter, r *http.Request) {
	// File path di SeaweedFS
	filePath := config.FilePanduanAplikasi

	// Ambil file dari SeaweedFS
	resp, err := app.readFromSeaweedFS(filePath)
	if err != nil {
		log.Debug().Msgf("Error reading file from SeaweedFS: %v", err)
		http.Error(w, "Failed to fetch file", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Atur header untuk download file
	fileName := path.Base(filePath) // Ambil nama file dari path
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))

	// Tulis konten file ke respons
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Debug().Msgf("Error writing file content: %v", err)
		http.Error(w, "Failed to download file", http.StatusInternalServerError)
		return
	}

	log.Info().Msgf("File %s downloaded successfully", fileName)
}

func (app *application) panduan2(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "https://youtu.be/4n97TBJwoZw", http.StatusSeeOther)
}

func (app *application) userActivities(w http.ResponseWriter, r *http.Request) {
	// Ambil session
	session, err := app.Store.Get(r, config.SessionName)
	if err != nil {
		http.Error(w, "Failed to get session", http.StatusInternalServerError)
		return
	}

	// Ambil pengguna ID dari session
	userID, ok := session.Values["user.id"].(string)
	if !ok || userID == "" {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}

	// Ambil parameter pagination
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1 // Default page
	}

	perPage, err := strconv.Atoi(r.URL.Query().Get("per_page"))
	if err != nil || perPage < 1 {
		perPage = 2 // Default items per page
	}

	// Fetch activities for the user
	activities, total, err := app.DB.GetUserActivities(userID, page, perPage)
	if err != nil {
		log.Debug().Msgf("Failed to get activities: %v", err)
		http.Error(w, "Failed to load activities", http.StatusInternalServerError)
		return
	}

	for _, activity := range activities {
		if activity["table_name"] == "app.permohonan" {
			var changes map[string]map[string]interface{}
			rawChanges := activity["changes"].(json.RawMessage) // Correct type assertion
			if err := json.Unmarshal(rawChanges, &changes); err == nil {
				activity["before"] = changes["before"]
				activity["after"] = changes["after"]
			} else {
				log.Debug().Msgf("Failed to parse changes: %v", err)
				activity["before"] = nil
				activity["after"] = nil
			}
		}
	}

	// Calculate pagination details
	totalPages := (total + perPage - 1) / perPage // Total number of pages
	hasPrevPage := page > 1
	hasNextPage := page < totalPages
	prevPage := page - 1
	nextPage := page + 1

	// Generate page range for pagination
	pageRange := 5
	startPage := max(1, page-(pageRange/2))
	endPage := min(totalPages, startPage+pageRange-1)

	if endPage-startPage < pageRange-1 {
		startPage = max(1, endPage-pageRange+1)
	}

	// Add ellipses handling for pagination
	pages := []int{}
	if startPage > 1 {
		pages = append(pages, 1) // Always show the first page
		if startPage > 2 {
			pages = append(pages, -1) // Ellipsis
		}
	}

	for i := startPage; i <= endPage; i++ {
		pages = append(pages, i)
	}

	if endPage < totalPages {
		if endPage < totalPages-1 {
			pages = append(pages, -1) // Ellipsis
		}
		pages = append(pages, totalPages) // Always show the last page
	}

	app.renderTemplate(w, "templates/user-activities.html", PageData{
		Data: map[string]interface{}{
			config.KeyBaseURL:            app.Host,
			config.KeyApplicationName:    config.ApplicationName,
			config.KeyApplicationVersion: config.ApplicationVersion,
			config.KeyApplicationOwner:   config.ApplicationOwner,
			config.KeyApplicationAuthor:  config.ApplicationAuthor,
			config.KeySessionAdminEmail:  session.Values["user.email"],
			config.KeySessionAdminNama:   session.Values["user.nama"],
			config.KeySessionAdminRole:   session.Values["user.role"],
			config.KeySessionAdminID:     session.Values["user.id"],
			"activities":                 activities, // Activities data
			"current_page":               page,
			"items_per_page":             perPage,
			"has_prev_page":              hasPrevPage,
			"has_next_page":              hasNextPage,
			"prev_page":                  prevPage,
			"next_page":                  nextPage,
			"pages":                      pages, // List of pages with ellipses
			"total_records":              total,
			"total_pages":                totalPages,
		},
	})

	return
}

func (app *application) updateLastActivity(w http.ResponseWriter, r *http.Request) {
	session, _ := app.Store.Get(r, config.SessionName)
	userID, ok := session.Values["user.id"].(string)

	if !ok || userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Perbarui waktu terakhir aktivitas di Redis
	client := app.redis
	lastActivityKey := fmt.Sprintf("user:%s:last_activity", userID)
	err := client.Set(context.Background(), lastActivityKey, time.Now().Unix(), 10*time.Minute).Err()
	if err != nil {
		log.Error().Err(err).Msg("Failed to update last activity in Redis")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (app *application) getOnlineUsers(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	keys, err := app.redis.Keys(ctx, "user:*:last_activity").Result()
	if err != nil {
		log.Error().Err(err).Msg("Error fetching keys from Redis")
		http.Error(w, "Failed to fetch online users", http.StatusInternalServerError)
		return
	}

	now := time.Now().Unix()
	threshold := 5 * 60 // 10 minutes threshold for activity

	var onlineUsers []map[string]interface{}

	for _, key := range keys {
		// Parse pengguna ID from Redis key
		parts := strings.Split(key, ":")
		if len(parts) < 3 {
			continue
		}
		userID := parts[1]

		// Get last activity timestamp
		lastActivityStr, err := app.redis.Get(ctx, key).Result()
		if err != nil {
			log.Warn().Err(err).Msgf("Error getting last activity for key %s", key)
			continue
		}

		lastActivity, err := strconv.ParseInt(lastActivityStr, 10, 64)
		if err != nil {
			log.Warn().Err(err).Msgf("Error parsing last activity for key %s", key)
			continue
		}

		// Check if pengguna is within activity threshold
		if now-lastActivity <= int64(threshold) {
			// Fetch pengguna details from the database
			userDetails, err := app.DB.GetUserByID(userID)
			if err != nil {
				log.Warn().Err(err).Msgf("Failed to fetch pengguna details for pengguna ID %s", userID)
				continue
			}

			// Add pengguna details to the online users list
			onlineUsers = append(onlineUsers, map[string]interface{}{
				"user_id":       userID,
				"email":         userDetails["email"],
				"nama":          userDetails["nama"],
				"role":          userDetails["role"],
				"jabatan":       userDetails["jabatan"],
				"last_activity": time.Unix(lastActivity, 0).Format("2006-01-02 15:04:05"), // Format timestamp
			})
		}
	}

	// Return as JSON
	_ = app.writeJSON(w, http.StatusOK, JSONResponse{
		Error:   false,
		Message: "Success",
		Data:    onlineUsers,
	})
}

func (app *application) getInventoryProgress(w http.ResponseWriter, r *http.Request) {
	progress, err := app.DB.GetInventoryProgress()
	if err != nil {
		log.Error().Err(err).Msg("Error fetching inventory progress")
		http.Error(w, "Failed to fetch inventory progress", http.StatusInternalServerError)
		return
	}

	// Return JSON response
	_ = app.writeJSON(w, http.StatusOK, JSONResponse{
		Error:   false,
		Message: "Success",
		Data:    progress,
	})
}

func (app *application) getInventoryProgressTrends(w http.ResponseWriter, r *http.Request) {
	trends, err := app.DB.GetInventoryProgressOverTime()
	if err != nil {
		log.Error().Err(err).Msg("Error fetching inventory progress trends")
		http.Error(w, "Failed to fetch inventory progress trends", http.StatusInternalServerError)
		return
	}

	_ = app.writeJSON(w, http.StatusOK, JSONResponse{
		Error:   false,
		Message: "Success",
		Data:    trends,
	})
}
