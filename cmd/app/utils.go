package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jung-kurt/gofpdf"
	"github.com/rs/zerolog/log"
	"html/template"
	"io"
	"mime/multipart"
	"monitor-loket/config"
	"monitor-loket/internal/models"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type RequestPayloadFile struct {
	FilePath string `json:"file_path"`
}

type JSONResponse struct {
	Error   bool        `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Meta    interface{} `json:"meta,omitempty"`
	Page    interface{} `json:"page,omitempty"`
}

func (app *application) writeJSON(w http.ResponseWriter, status int, data interface{}, headers ...http.Header) error {
	out, err := json.MarshalIndent(data, "", "")
	if err != nil {
		return err
	}

	if len(headers) > 0 {
		for key, value := range headers[0] {
			w.Header()[key] = value
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_, err = w.Write(out)
	if err != nil {
		return err
	}

	return nil
}

func (app *application) readJSON(w http.ResponseWriter, r *http.Request, data interface{}) error {
	maxBytes := 1048576 // 1MB

	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes))

	dec := json.NewDecoder(r.Body)

	dec.DisallowUnknownFields()

	err := dec.Decode(data)

	if err != nil {
		return err
	}

	err = dec.Decode(&struct{}{})

	if err != io.EOF {
		return errors.New("body must only have a single JSON value")
	}

	return nil

}

func (app *application) errorJSON(w http.ResponseWriter, err error, status ...int) error {
	statusCode := http.StatusBadRequest

	if len(status) > 0 {
		statusCode = status[0]
	}

	var payload JSONResponse
	payload.Error = true
	payload.Message = err.Error()

	return app.writeJSON(w, statusCode, payload)
}

func (app *application) initPaginationResponse(r *http.Request) (int, int, string, string) {
	// get the page and per_page query params
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}

	perPage, err := strconv.Atoi(r.URL.Query().Get("per_page"))
	if err != nil || perPage < 1 {
		perPage = 10
	}

	sort := r.URL.Query().Get("sort")
	if sort == "" {
		sort = "created_at"
	}

	order := r.URL.Query().Get("order")
	if order == "" {
		order = "DESC"
	}

	if order == "desc" {
		order = "DESC"
	} else {
		order = "ASC"
	}

	return page, perPage, sort, order
}

func (app *application) createPaginationResponse(data []interface{}, totalItems, totalPages, page, perPage int, sort, order string) JSONResponse {

	response := JSONResponse{
		Data: data,
		Page: map[string]interface{}{
			"total":      totalItems,
			"total_page": totalPages,
			"current":    page,
			"per_page":   perPage,
			"from":       (page-1)*perPage + 1,
			"to":         page * perPage,
			"sort":       sort,
			"order":      order,
		},
		Meta: map[string]interface{}{
			"application": config.ApplicationName,
			"version":     config.ApplicationVersion,
			"author":      config.ApplicationAuthor,
		},
	}

	return response
}

func (app *application) GetIpAddrAndUserAgent(r *http.Request) (string, string) {
	var ipAddr string
	var userAgent string

	ipAddr = r.RemoteAddr
	userAgent = r.Header.Get("User-Agent")

	return ipAddr, userAgent
}

func (app *application) createJSONResponse(data interface{}, e bool, msg ...string) JSONResponse {
	var respMsg string

	if msg != nil {
		respMsg = msg[0]
	}

	response := JSONResponse{
		Data: data,
		Meta: map[string]interface{}{
			"application": config.ApplicationName,
			"version":     config.ApplicationVersion,
			"author":      config.ApplicationAuthor,
		},
		Error:   e,
		Message: respMsg,
	}

	return response
}

func generateAPIKey(length int) (string, error) {
	// Determine the number of bytes needed based on the desired length
	numBytes := length * 3 / 4 // Base64 encoding uses 4 characters for every 3 bytes

	// Generate random bytes
	randomBytes := make([]byte, numBytes)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", err
	}

	// Encode random bytes to Base64
	apiKey := base64.URLEncoding.EncodeToString(randomBytes)

	// Trim padding characters (=) from the end of the string
	apiKey = apiKey[:length]

	return apiKey, nil
}

func (app *application) failOnError(err error, msg string) {
	if err != nil {
		log.Err(err).Msg(msg)
	}
}

func (app *application) pointerString(s string) *string {
	return &s
}

type PageData struct {
	Data map[string]interface{}
}

func (app *application) renderTemplate(w http.ResponseWriter, tmpl string, data PageData) {

	// Parse the HTML template file and attach custom functions
	t, err := template.New(app.getStringAfterLastSlash(tmpl)).Funcs(template.FuncMap{
		"toJSON":           app.toJSON, // Pass app.toJSON as a function
		"convertTimestamp": app.ConvertTimestamp,
		"formatTime":       app.formatTime,
		"add": func(a, b int) int {
			return a + b
		},
		"highlightText": app.highlightText,
	}).ParseFiles(tmpl)
	if err != nil {
		_ = app.errorJSON(w, err)
		return
	}

	// Execute the template with data and write the result to the response
	err = t.Execute(w, data)
	if err != nil {
		_ = app.errorJSON(w, err)
	}
}

// readFromSeaweedFS reads a file from SeaweedFS via the Filer's proxy URL
func (app *application) readFromSeaweedFS(filePath string) (*http.Response, error) {
	// Pastikan base URL tidak memiliki garis miring di akhir
	base := strings.TrimRight(app.SeaweedFSFilerBaseURL, "/")
	// Gabungkan URL dengan path file
	seaweedFilerURL := fmt.Sprintf("%s/%s", base, strings.TrimLeft(filePath, "/"))
	log.Debug().Msgf("Fetching file from SeaweedFS: %s", seaweedFilerURL)

	// Kirim permintaan ke SeaweedFS
	resp, err := http.Get(seaweedFilerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SeaweedFS: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, fmt.Errorf("file not found on SeaweedFS, status code: %d", resp.StatusCode)
	}

	return resp, nil
}

// uploadToSeaweedFS uploads a file to SeaweedFS via the Filer's proxy URL
func (app *application) uploadToSeaweedFS(filePath string, file io.Reader, fileSize int64) (*http.Response, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filePath)
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return nil, err
	}
	err = writer.Close()
	if err != nil {
		return nil, err
	}

	seaweedFilerURL := fmt.Sprintf("%s/%s", app.SeaweedFSFilerBaseURL, filePath)
	req, err := http.NewRequest("POST", seaweedFilerURL, body)
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Uploading file to SeaweedFS: %s", seaweedFilerURL)

	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// toJSON converts a Go object to a JSON string.
func (app *application) toJSON(v interface{}) string {
	jsonData, err := json.Marshal(v)
	if err != nil {
		log.Debug().Err(err).Msg("failed to marshal JSON")
		return "{}" // Return empty JSON object on error
	}
	return string(jsonData)
}

// getStringAfterLastSlash returns the substring after the last '/' in the input string.
func (app *application) getStringAfterLastSlash(input string) string {
	// Use strings.LastIndex to find the last occurrence of '/'
	lastSlashIndex := strings.LastIndex(input, "/")
	if lastSlashIndex == -1 {
		// If '/' is not found, return the entire input string
		return input
	}
	// Return the substring after the last '/'
	return input[lastSlashIndex+1:]
}

// ConvertTimestamp mengonversi format timestamp ISO 8601 menjadi yyyy-mm-dd HH:ii:ss
func (app *application) ConvertTimestamp(input string) (string, error) {
	// Format input sesuai ISO 8601
	const inputFormat = "2006-01-02T15:04:05.999999-07:00"
	// Format output yang diinginkan
	const outputFormat = "2006-01-02 15:04:05"

	// Parsing waktu dari string input
	parsedTime, err := time.Parse(inputFormat, input)
	if err != nil {
		return "", fmt.Errorf("error parsing time: %w", err)
	}

	// Format waktu ke format output
	return parsedTime.Format(outputFormat), nil
}

func (app *application) formatTime(t time.Time) string {
	return t.Format("2006-01-02T15:04:05.999999-07:00")
}

func (app *application) generatePDF(records []models.PermohonanRecord) *gofpdf.Fpdf {
	// Inisialisasi PDF dengan ukuran A4 landscape
	pdf := gofpdf.New("L", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "B", 12)

	// Judul PDF
	pdf.Cell(277, 10, "Laporan Monitoring dan Pelaporan Permohonan per Tanggal "+time.Now().Format("02 January 2006"))
	pdf.Ln(12)

	// Header Tabel
	headers := []string{
		"ID", "Dikuasakan", "Nama Kuasa", "Nomor Berkas", "Telepon",
		"Nama Pemohon", "Jenis Permohonan", "PPAT", "Dibuat Tanggal",
		"Dibuat Oleh", "Diperbarui Tanggal", "Diperbarui Oleh",
	}

	// Set lebar kolom
	colWidths := []float64{35, 20, 35, 35, 25, 35, 35, 25, 35, 35, 35, 35}

	// Tambahkan Header ke PDF
	pdf.SetFont("Arial", "B", 8)
	for i, header := range headers {
		pdf.CellFormat(colWidths[i], 10, header, "1", 0, "C", false, 0, "")
	}
	pdf.Ln(-1)

	// Isi Tabel
	pdf.SetFont("Arial", "", 8)
	for _, record := range records {
		data := []string{
			record.ID, strconv.FormatBool(record.Dikuasakan), record.NamaKuasa, record.NomorBerkas, record.Phone,
			record.NamaPemohon, record.JenisPermohonan, record.PPAT, record.CreatedAt,
			record.CreatedBy,
			func() string {
				if record.UpdatedAt != "" {
					return record.UpdatedAt
				}
				return "-"
			}(),
			func() string {
				if record.UpdatedBy != "" {
					return record.UpdatedBy
				}
				return "-"
			}(),
		}

		// Word Wrap untuk setiap cell
		yBefore := pdf.GetY()
		rowHeight := 4.0
		for i, value := range data {
			lines := pdf.SplitLines([]byte(value), colWidths[i])
			cellHeight := float64(len(lines)) * rowHeight
			pdf.MultiCell(colWidths[i], rowHeight, value, "1", "L", false)

			// Mengatur posisi kolom berikutnya di baris yang sama
			if i < len(headers)-1 {
				pdf.SetXY(pdf.GetX()+colWidths[i], yBefore)
			} else {
				pdf.Ln(cellHeight) // Pindah ke baris berikutnya
			}
		}
	}

	return pdf
}

// highlightText membungkus teks yang cocok dengan elemen <span> berwarna kuning.
func (app *application) highlightText(input, search string) template.HTML {
	if search == "" {
		return template.HTML(template.HTMLEscapeString(input))
	}
	search = strings.ToLower(search)
	inputLower := strings.ToLower(input)

	// Temukan posisi kecocokan
	index := strings.Index(inputLower, search)
	if index == -1 {
		return template.HTML(template.HTMLEscapeString(input))
	}

	// Bungkus teks yang cocok dengan <span>
	highlighted := input[:index] +
		`<span style="background-color: #FFFACD;">` + input[index:index+len(search)] + `</span>` +
		input[index+len(search):]

	return template.HTML(highlighted)
}
