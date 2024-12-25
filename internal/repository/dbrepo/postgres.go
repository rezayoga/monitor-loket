package dbrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgtype"
	"github.com/oklog/ulid/v2"
	"github.com/rs/zerolog/log"
	"monitor-loket/config"
	"monitor-loket/internal/models"
	"os"
	"strings"
	"time"

	"github.com/google/uuid" // Tambahkan ini untuk UUID
)

type PostgresDBRepo struct {
	DB *sql.DB
}

const dbTimeout = time.Second * 3

func (m *PostgresDBRepo) Connection() *sql.DB {
	return m.DB
}

func (m *PostgresDBRepo) CreateMessage(data map[string]interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	mid := data["mid"].(string)
	to := data["to"].(string)
	requestBody := data["request_body"].(string)
	isError := data["is_error"].(bool)
	responseBody := data["response_body"].(string)
	phoneNumberID := data["phone_number_id"].(string)
	tunggakanID := data["tunggakan_id"].(string)

	//log.Println("mid: ", mid)
	//log.Println("wa_id: ", to)
	//log.Println("request_body: ", requestBody)
	//log.Println("is_error: ", isError)
	//log.Println("response_body: ", responseBody)
	//log.Println("phone_number_id: ", phoneNumberID)

	query := fmt.Sprintf(`insert into broadcast.messages (id, mid, wa_id, request_body, created_at, is_error, response_body, phone_number_id, tunggakan_id) values ('%s', '%s', '%s', '%s'::jsonb, '%s', %t, '%s'::jsonb, '%s', '%s')`,
		ulid.Make(), mid, to, requestBody, time.Now().Format(time.RFC3339), isError, responseBody, phoneNumberID, tunggakanID)

	_, err := m.DB.ExecContext(ctx, query)

	//log.Println("query: ", query)

	if err != nil {
		return err
	}

	return nil
}

func (m *PostgresDBRepo) Login(email, password string) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	query := `select id, phone, email, nip, nama, jabatan, role, is_active from public.users where email = $1 and password = $2
and is_active = true`

	row := m.DB.QueryRowContext(ctx, query, email, password)

	var id, phone, email_, nip, nama, jabatan, role string
	var isActive bool

	err := row.Scan(&id, &phone, &email_, &nip, &nama, &jabatan, &role, &isActive)

	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"id":        id,
		"phone":     phone,
		"email":     email_,
		"nip":       nip,
		"nama":      nama,
		"jabatan":   jabatan,
		"role":      role,
		"is_active": isActive,
	}, nil
}

func (m *PostgresDBRepo) LoginCheckUserIsActive(email string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	query := `select is_active from public.users where email = $1`

	row := m.DB.QueryRowContext(ctx, query, email)

	var isActive bool

	err := row.Scan(&isActive)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Debug().Msgf("No user found with email: %s", email)
			return false, nil // Tidak aktif karena user tidak ditemukan
		}
		log.Debug().Msgf("Error checking user active: %v", err)
		return false, err // Kesalahan lain dilaporkan
	}

	return isActive, nil
}

func normalizeString(value string) string {
	return strings.TrimSpace(value)
}

func (m *PostgresDBRepo) CreatePermohonan(records []map[string]interface{}) ([]string, error) {
	if len(records) == 0 {
		log.Debug().Msg("No records to insert")
		return nil, nil
	}

	// Query dasar dengan placeholder
	query := `
		INSERT INTO app.permohonan (
			dikuasakan, nama_kuasa, nomor_berkas, phone, nama_pemohon, jenis_permohonan, ppat, created_at, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		) RETURNING id`

	var insertedIDs []string

	// Loop untuk setiap record
	for idx, record := range records {
		// Persiapkan data
		values := []interface{}{
			record["dikuasakan"],       // Boolean
			record["nama_kuasa"],       // Text
			record["nomor_berkas"],     // Varchar(64)
			record["phone"],            // Varchar(32)
			record["nama_pemohon"],     // Text
			record["jenis_permohonan"], // Text
			record["ppat"],             // Text
			time.Now(),                 // Timestamp (created_at)
			record["created_by"],       // Text (UUID pengguna)
		}

		// Debug log untuk record yang sedang diproses
		log.Debug().Msgf("Processing record #%d: %+v", idx+1, record)
		log.Debug().Msgf("Query: %s", query)
		log.Debug().Msgf("Values: %+v", values)

		// Eksekusi query
		var insertedID string
		err := m.DB.QueryRowContext(context.Background(), query, values...).Scan(&insertedID)
		if err != nil {
			// Log error dengan informasi tambahan
			log.Error().Msgf("Error inserting record #%d: %v, Record: %+v", idx+1, err, record)
			return nil, fmt.Errorf("error inserting record #%d: %w", idx+1, err)
		}

		// Simpan ID yang dihasilkan
		insertedIDs = append(insertedIDs, insertedID)

		// Log sukses jika record berhasil dimasukkan
		log.Debug().Msgf("Successfully inserted record #%d with ID: %s", idx+1, insertedID)
	}

	return insertedIDs, nil
}

func (m *PostgresDBRepo) GetAllPermohonan(page, perPage int, sort, order, search string) ([]map[string]interface{}, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	// Default values for sort and order
	if sort == "" {
		sort = "p.created_at"
	}
	if order == "" {
		order = "desc"
	}

	// Search query condition
	searchCondition := ""
	if search != "" {
		searchCondition = fmt.Sprintf(`
        AND (
            p.id::text ILIKE '%%%s%%' OR
            p.nama_pemohon ILIKE '%%%s%%' OR
            p.nama_kuasa ILIKE '%%%s%%' OR
            p.nomor_berkas ILIKE '%%%s%%' OR
            p.phone ILIKE '%%%s%%' OR
            p.jenis_permohonan ILIKE '%%%s%%' OR
            p.ppat ILIKE '%%%s%%' OR
			p.created_by ILIKE '%%%s%%' OR
			u1.nama ILIKE '%%%s%%' OR
			u2.nama ILIKE '%%%s%%'
        )`, search, search, search, search, search, search, search, search, search, search)
	}

	// Pagination
	limit := perPage
	offset := (page - 1) * perPage

	// Query to fetch total count
	countQuery := fmt.Sprintf(`SELECT COUNT(p.id) FROM app.permohonan p
                   LEFT JOIN public.users u1 ON p.created_by::uuid = u1.id::uuid
        			LEFT JOIN public.users u2 ON p.updated_by::uuid = u2.id::uuid
                   WHERE 1=1 %s`, searchCondition)

	var totalCount int
	err := m.DB.QueryRowContext(ctx, countQuery).Scan(&totalCount)
	if err != nil {
		return nil, 0, err
	}

	// Query to fetch data
	query := fmt.Sprintf(`
        SELECT 
            p.id,
            p.dikuasakan,
            COALESCE(p.nama_kuasa, '') AS nama_kuasa,
            COALESCE(p.nomor_berkas, '') AS nomor_berkas,
            COALESCE(p.phone, '') AS phone,
            COALESCE(p.nama_pemohon, '') AS nama_pemohon,
            COALESCE(p.jenis_permohonan, '') AS jenis_permohonan,
            COALESCE(p.ppat, '') AS ppat,
            COALESCE(p.created_at, '2000-01-01 00:00:00') AS created_at,
            COALESCE(p.created_by, '') AS created_by,
            COALESCE(p.updated_at, '2000-01-01 00:00:00') AS updated_at,
            COALESCE(p.updated_by, '') AS updated_by,
            COALESCE(u1.nama, '') AS created_by_name,
            COALESCE(u2.nama, '') AS updated_by_name
        FROM app.permohonan p
        LEFT JOIN public.users u1 ON p.created_by::uuid = u1.id::uuid
        LEFT JOIN public.users u2 ON p.updated_by::uuid = u2.id::uuid
        WHERE 1=1 %s
        ORDER BY %s %s
        LIMIT %d OFFSET %d`, searchCondition, sort, order, limit, offset)

	rows, err := m.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Error().Msgf("Error closing rows: %v", err)
		}
	}(rows)

	// Process the rows
	var permohonans []map[string]interface{}
	for rows.Next() {
		var id, namaKuasa, nomorBerkas, phone, namaPemohon, jenisPermohonan, ppat, createdBy, updatedBy, createdByNama, updatedByNama string
		var dikuasakan bool
		var createdAt, updatedAt time.Time
		err = rows.Scan(
			&id, &dikuasakan, &namaKuasa, &nomorBerkas, &phone, &namaPemohon, &jenisPermohonan, &ppat, &createdAt, &createdBy, &updatedAt, &updatedBy,
			&createdByNama, &updatedByNama,
		)
		if err != nil {
			return nil, 0, err
		}

		permohonans = append(permohonans, map[string]interface{}{
			"id":               id,
			"dikuasakan":       dikuasakan,
			"nama_kuasa":       namaKuasa,
			"nomor_berkas":     nomorBerkas,
			"phone":            phone,
			"nama_pemohon":     namaPemohon,
			"jenis_permohonan": jenisPermohonan,
			"ppat":             ppat,
			"created_at":       createdAt,
			"created_by":       createdBy,
			"updated_at":       updatedAt,
			"updated_by":       updatedBy,
			"created_by_nama":  createdByNama,
			"updated_by_nama":  updatedByNama,
		})
	}

	if err = rows.Err(); err != nil {
		return nil, 0, err
	}

	return permohonans, totalCount, nil
}

func (m *PostgresDBRepo) GetAllPermohonanByUserID(page, perPage int, sort, order, search, userID string) ([]map[string]interface{}, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	// Default values for sort and order
	if sort == "" {
		sort = "p.created_at"
	}
	if order == "" {
		order = "desc"
	}

	// Search query condition
	searchCondition := ""
	if search != "" {
		searchCondition = fmt.Sprintf(`
        AND (
            p.id::text ILIKE '%%%s%%' OR
            p.nama_pemohon ILIKE '%%%s%%' OR
            p.nama_kuasa ILIKE '%%%s%%' OR
            p.nomor_berkas ILIKE '%%%s%%' OR
            p.phone ILIKE '%%%s%%' OR
            p.jenis_permohonan ILIKE '%%%s%%' OR
            p.ppat ILIKE '%%%s%%' OR
			p.created_by ILIKE '%%%s%%' OR
			u1.nama ILIKE '%%%s%%' OR
			u2.nama ILIKE '%%%s%%'
        )`, search, search, search, search, search, search, search, search, search, search)
	}

	// Pagination
	limit := perPage
	offset := (page - 1) * perPage

	// Query to fetch total count
	countQuery := fmt.Sprintf(`SELECT COUNT(p.id) FROM app.permohonan p 
                   LEFT JOIN public.users u1 ON p.created_by::uuid = u1.id::uuid
        LEFT JOIN public.users u2 ON p.updated_by::uuid = u2.id::uuid
                   WHERE created_by = '%s' %s`, userID, searchCondition)

	var totalCount int
	err := m.DB.QueryRowContext(ctx, countQuery).Scan(&totalCount)
	if err != nil {
		return nil, 0, err
	}

	// Query to fetch data
	query := fmt.Sprintf(`
        SELECT 
            p.id,
            p.dikuasakan,
            COALESCE(p.nama_kuasa, '') AS nama_kuasa,
            COALESCE(p.nomor_berkas, '') AS nomor_berkas,
            COALESCE(p.phone, '') AS phone,
            COALESCE(p.nama_pemohon, '') AS nama_pemohon,
            COALESCE(p.jenis_permohonan, '') AS jenis_permohonan,
            COALESCE(p.ppat, '') AS ppat,
            COALESCE(p.created_at, '2000-01-01 00:00:00') AS created_at,
            COALESCE(p.created_by, '') AS created_by,
            COALESCE(p.updated_at, '2000-01-01 00:00:00') AS updated_at,
            COALESCE(p.updated_by, '') AS updated_by,
            COALESCE(u1.nama, '') AS created_by_name,
            COALESCE(u2.nama, '') AS updated_by_name
        FROM app.permohonan p
        LEFT JOIN public.users u1 ON p.created_by::uuid = u1.id::uuid
        LEFT JOIN public.users u2 ON p.updated_by::uuid = u2.id::uuid
        WHERE created_by = '%s' %s
        ORDER BY %s %s
        LIMIT %d OFFSET %d`, userID, searchCondition, sort, order, limit, offset)

	log.Debug().Msgf("Query: %s", query)

	rows, err := m.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Error().Msgf("Error closing rows: %v", err)
		}
	}(rows)

	// Process the rows
	var permohonans []map[string]interface{}
	for rows.Next() {
		var id, namaKuasa, nomorBerkas, phone, namaPemohon, jenisPermohonan, ppat, createdBy, updatedBy, createdByNama, updatedByNama string
		var dikuasakan bool
		var createdAt, updatedAt time.Time
		err = rows.Scan(
			&id, &dikuasakan, &namaKuasa, &nomorBerkas, &phone, &namaPemohon, &jenisPermohonan, &ppat, &createdAt, &createdBy, &updatedAt, &updatedBy,
			&createdByNama, &updatedByNama,
		)
		if err != nil {
			return nil, 0, err
		}

		permohonans = append(permohonans, map[string]interface{}{
			"id":               id,
			"dikuasakan":       dikuasakan,
			"nama_kuasa":       namaKuasa,
			"nomor_berkas":     nomorBerkas,
			"phone":            phone,
			"nama_pemohon":     namaPemohon,
			"jenis_permohonan": jenisPermohonan,
			"ppat":             ppat,
			"created_at":       createdAt,
			"created_by":       createdBy,
			"updated_at":       updatedAt,
			"updated_by":       updatedBy,
			"created_by_nama":  createdByNama,
			"updated_by_nama":  updatedByNama,
		})
	}

	if err = rows.Err(); err != nil {
		return nil, 0, err
	}

	return permohonans, totalCount, nil
}

func (m *PostgresDBRepo) GetPermohonanByID(id string) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	query := `
		SELECT 
			COALESCE(p.id::text, '') AS id,
			COALESCE(p.dikuasakan, false) AS dikuasakan,
			COALESCE(p.nama_kuasa, '') AS nama_kuasa,
			COALESCE(p.nomor_berkas, '') AS nomor_berkas,
			COALESCE(p.phone, '') AS phone,
			COALESCE(p.nama_pemohon, '') AS nama_pemohon,
			COALESCE(p.jenis_permohonan, '') AS jenis_permohonan,
			COALESCE(p.ppat, '') AS ppat,
			COALESCE(p.created_at, '2000-01-01 00:00:00') AS created_at,
			COALESCE(p.created_by, '') AS created_by,
			COALESCE(p.updated_at, '2000-01-01 00:00:00') AS updated_at,
			COALESCE(p.updated_by, '') AS updated_by,
			COALESCE(u1.nama, '') AS created_by_name,
			COALESCE(u2.nama, '') AS updated_by_name
		FROM app.permohonan p
		LEFT JOIN public.users u1 ON p.created_by::uuid = u1.id::uuid
			LEFT JOIN public.users u2 ON p.updated_by::uuid = u2.id::uuid
		WHERE p.id = $1`

	row := m.DB.QueryRowContext(ctx, query, id)

	var permohonanID, namaKuasa, nomorBerkas, phone, namaPemohon, jenisPermohonan, ppat, createdBy, updatedBy, createdByNama, updatedByNama string
	var dikuasakan bool
	var createdAt, updatedAt time.Time

	err := row.Scan(
		&permohonanID,
		&dikuasakan,
		&namaKuasa,
		&nomorBerkas,
		&phone,
		&namaPemohon,
		&jenisPermohonan,
		&ppat,
		&createdAt,
		&createdBy,
		&updatedAt,
		&updatedBy,
		&createdByNama,
		&updatedByNama,
	)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"id":               permohonanID,
		"dikuasakan":       dikuasakan,
		"nama_kuasa":       namaKuasa,
		"nomor_berkas":     nomorBerkas,
		"phone":            phone,
		"nama_pemohon":     namaPemohon,
		"jenis_permohonan": jenisPermohonan,
		"ppat":             ppat,
		"created_at":       createdAt,
		"created_by":       createdBy,
		"updated_at":       updatedAt,
		"updated_by":       updatedBy,
		"created_by_nama":  createdByNama,
		"updated_by_nama":  updatedByNama,
	}, nil
}

// UpdatePermohonanByID updates an permohonan in the database by ID.
func (m *PostgresDBRepo) UpdatePermohonanByID(id string, data map[string]interface{}) error {
	query := `
		UPDATE app.permohonan
		SET 
			dikuasakan = COALESCE($1, dikuasakan),
			nama_kuasa = COALESCE($2, nama_kuasa),
			nomor_berkas = COALESCE($3, nomor_berkas),
			phone = COALESCE($4, phone),
			nama_pemohon = COALESCE($5, nama_pemohon),
			jenis_permohonan = COALESCE($6, jenis_permohonan),
			ppat = COALESCE($7, ppat),
			updated_at = COALESCE($8, updated_at),
			updated_by = COALESCE($9, updated_by)
		WHERE id = $10`

	_, err := m.DB.ExecContext(context.Background(), query,
		data["dikuasakan"],
		data["nama_kuasa"],
		data["nomor_berkas"],
		data["phone"],
		data["nama_pemohon"],
		data["jenis_permohonan"],
		data["ppat"],
		data["updated_at"],
		data["updated_by"],
		id,
	)

	log.Debug().Msgf("Data: %+v", data)

	return err
}

// Prettify JSON helper function
func prettifyJSON(data string) string {
	var out map[string]interface{}
	err := json.Unmarshal([]byte(data), &out)
	if err != nil {
		log.Debug().Msgf("Failed to unmarshal JSON: %v", err)
		return data
	}

	formatted, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		log.Debug().Msgf("Failed to marshal JSON: %v", err)
		return data
	}

	return string(formatted)
}

func (m *PostgresDBRepo) UpdatePassword(userID, newPassword string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	query := `
		UPDATE public.users
		SET
			password = $1
		WHERE id = $2`

	_, err := m.DB.ExecContext(ctx, query, newPassword, userID)
	if err != nil {
		return err
	}

	return nil
}

func (m *PostgresDBRepo) LogActivity(activity map[string]interface{}) error {
	query := `
		INSERT INTO app.activities (
			id, user_id, table_name, record_id, action, description, changes, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP
		)`

	activityID := uuid.New().String()

	_, err := m.DB.ExecContext(context.Background(), query,
		activityID,
		activity["user_id"],
		activity["table_name"],
		activity["record_id"],
		activity["action"],
		activity["description"],
		activity["changes"],
	)
	if err != nil {
		log.Err(err).Msg("Failed to log activity")
		return err
	}

	log.Debug().Msgf("Activity logged with ID: %s", activityID)
	return nil
}

func (m *PostgresDBRepo) HardDeletePermohonan(permohonanID string) error {
	query := `DELETE FROM app.permohonan WHERE id = $1`

	result, err := m.DB.ExecContext(context.Background(), query, permohonanID)
	if err != nil {
		log.Err(err).Msgf("Failed to hard delete permohonan with ID %s", permohonanID)
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Err(err).Msg("Failed to get rows affected count")
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no rows deleted, permohonan might not exist")
	}

	log.Debug().Msgf("Successfully hard deleted permohonan with ID %s", permohonanID)
	return nil
}

func (m *PostgresDBRepo) GetAllUsers(page, perPage int, sort, order, search string) ([]map[string]interface{}, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	var systemMail string = os.Getenv("SYSTEM_MAIL")

	// Default values for sort and order
	if sort == "" {
		sort = "u.created_at"
	}
	if order == "" {
		order = "desc"
	}

	// Search query condition
	searchCondition := ""
	if search != "" {
		searchCondition = fmt.Sprintf(`
		AND (
			u.id::text ILIKE '%%%s%%' OR
			u.phone ILIKE '%%%s%%' OR
			u.email ILIKE '%%%s%%' OR
			u.nip ILIKE '%%%s%%' OR
			u.nama ILIKE '%%%s%%' OR
			u.jabatan ILIKE '%%%s%%' OR
			u.role ILIKE '%%%s%%'
		)`, search, search, search, search, search, search)
	}

	// Pagination
	limit := perPage
	offset := (page - 1) * perPage

	// Query to fetch total count
	countQuery := fmt.Sprintf(`SELECT COUNT(u.id) FROM public.users u WHERE u.email != '%s' %s`, systemMail, searchCondition)

	var totalCount int
	err := m.DB.QueryRowContext(ctx, countQuery).Scan(&totalCount)
	if err != nil {
		log.Debug().Msgf("Error querying the database for count: %v", err)
		return nil, 0, err
	}

	// Query to fetch data
	query := fmt.Sprintf(`
		SELECT 
			u.id,
			u.phone,
			u.email,
			u.nip,
			u.nama,
			u.jabatan,
			u.is_active,
			u.role,
			COALESCE(u.created_at, '2000-01-01 00:00:00') AS created_at,
			COALESCE(u.updated_at, '2000-01-01 00:00:00') AS updated_at,
			COALESCE(p.permission_list, '[]') AS permissions
		FROM public.users u
		LEFT JOIN (
			SELECT up.user_id, JSON_AGG(JSON_BUILD_OBJECT(
				'id', p.id,
				'slug', p.slug,
				'description', p.description,
			    'method', p.method
			)) AS permission_list
			FROM public.user_permissions up
			JOIN public.permissions p ON up.permission_id = p.id
			GROUP BY up.user_id
		) p ON u.id = p.user_id
		WHERE u.email != '%s' %s
		ORDER BY %s %s 
		LIMIT %d OFFSET %d`, systemMail, searchCondition, sort, order, limit, offset)

	//log.Debug().Msgf("Query: %s", query)

	rows, err := m.DB.QueryContext(ctx, query)
	if err != nil {
		log.Debug().Msgf("Error querying the database: %v", err)
		return nil, 0, err
	}
	defer rows.Close()

	// Process the rows
	var users []map[string]interface{}
	for rows.Next() {
		var id, phone, email, nip, nama, jabatan, role, permissions string
		var isActive bool
		var createdAt, updatedAt time.Time

		// Scan values into temporary variables
		err := rows.Scan(&id, &phone, &email, &nip, &nama, &jabatan, &isActive, &role, &createdAt, &updatedAt, &permissions)
		if err != nil {
			return nil, 0, err
		}

		// Parse permissions as JSON
		var parsedPermissions []map[string]interface{}
		err = json.Unmarshal([]byte(permissions), &parsedPermissions)
		if err != nil {
			return nil, 0, err
		}

		// Add values to the user map
		users = append(users, map[string]interface{}{
			"id":          id,
			"phone":       phone,
			"email":       email,
			"nip":         nip,
			"nama":        nama,
			"jabatan":     jabatan,
			"is_active":   isActive,
			"role":        role,
			"created_at":  createdAt,
			"updated_at":  updatedAt,
			"permissions": parsedPermissions,
		})
	}

	if err = rows.Err(); err != nil {
		return nil, 0, err
	}

	return users, totalCount, nil
}

func (m *PostgresDBRepo) GetAllUsersExceptKakan(page, perPage int, sort, order, search string) ([]map[string]interface{}, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	var systemMail string = os.Getenv("SYSTEM_MAIL")

	// Default values for sort and order
	if sort == "" {
		sort = "u.created_at"
	}
	if order == "" {
		order = "desc"
	}

	// Search query condition
	searchCondition := ""
	if search != "" {
		searchCondition = fmt.Sprintf(`
		AND (
			u.id::text ILIKE '%%%s%%' OR
			u.phone ILIKE '%%%s%%' OR
			u.email ILIKE '%%%s%%' OR
			u.nip ILIKE '%%%s%%' OR
			u.nama ILIKE '%%%s%%' OR
			u.jabatan ILIKE '%%%s%%' OR
			u.role ILIKE '%%%s%%'
		)`, search, search, search, search, search, search)
	}

	// Pagination
	limit := perPage
	offset := (page - 1) * perPage

	// Query to fetch total count
	countQuery := fmt.Sprintf(`SELECT COUNT(u.id) FROM public.users u WHERE u.email != '%s' AND role != '%s' %s`, systemMail, config.RoleKepalaKantor, searchCondition)

	var totalCount int
	err := m.DB.QueryRowContext(ctx, countQuery).Scan(&totalCount)
	if err != nil {
		log.Debug().Msgf("Error querying the database for count: %v", err)
		return nil, 0, err
	}

	// Query to fetch data
	query := fmt.Sprintf(`
		SELECT 
			u.id,
			u.phone,
			u.email,
			u.nip,
			u.nama,
			u.jabatan,
			u.is_active,
			u.role,
			COALESCE(u.created_at, '2000-01-01 00:00:00') AS created_at,
			COALESCE(u.updated_at, '2000-01-01 00:00:00') AS updated_at,
			COALESCE(p.permission_list, '[]') AS permissions
		FROM public.users u
		LEFT JOIN (
			SELECT up.user_id, JSON_AGG(JSON_BUILD_OBJECT(
				'id', p.id,
				'slug', p.slug,
				'description', p.description,
			    'method', p.method
			)) AS permission_list
			FROM public.user_permissions up
			JOIN public.permissions p ON up.permission_id = p.id
			GROUP BY up.user_id
		) p ON u.id = p.user_id
		WHERE u.email != '%s' AND role != '%s' %s
		ORDER BY %s %s 
		LIMIT %d OFFSET %d`, systemMail, config.RoleKepalaKantor, searchCondition, sort, order, limit, offset)

	//log.Debug().Msgf("Query: %s", query)

	rows, err := m.DB.QueryContext(ctx, query)
	if err != nil {
		log.Debug().Msgf("Error querying the database: %v", err)
		return nil, 0, err
	}
	defer rows.Close()

	// Process the rows
	var users []map[string]interface{}
	for rows.Next() {
		var id, phone, email, nip, nama, jabatan, role, permissions string
		var isActive bool
		var createdAt, updatedAt time.Time

		// Scan values into temporary variables
		err := rows.Scan(&id, &phone, &email, &nip, &nama, &jabatan, &isActive, &role, &createdAt, &updatedAt, &permissions)
		if err != nil {
			return nil, 0, err
		}

		// Parse permissions as JSON
		var parsedPermissions []map[string]interface{}
		err = json.Unmarshal([]byte(permissions), &parsedPermissions)
		if err != nil {
			return nil, 0, err
		}

		// Add values to the user map
		users = append(users, map[string]interface{}{
			"id":          id,
			"phone":       phone,
			"email":       email,
			"nip":         nip,
			"nama":        nama,
			"jabatan":     jabatan,
			"is_active":   isActive,
			"role":        role,
			"created_at":  createdAt,
			"updated_at":  updatedAt,
			"permissions": parsedPermissions,
		})
	}

	if err = rows.Err(); err != nil {
		return nil, 0, err
	}

	return users, totalCount, nil
}

func (m *PostgresDBRepo) GetUserByID(id string) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	query := `
		SELECT 
			u.nama,
			u.email,
			u.phone,
			u.nip,
			u.jabatan,
			u.role,
			u.is_active,
			COALESCE(p.permission_list, '[]') AS permissions,
			u.created_at,
			u.updated_at
		FROM public.users u
		LEFT JOIN (
			SELECT up.user_id, JSON_AGG(JSON_BUILD_OBJECT(
				'id', p.id,
				'slug', p.slug,
				'description', p.description,
			    'method', p.method
			)) AS permission_list
			FROM public.user_permissions up
			JOIN public.permissions p ON up.permission_id = p.id
			GROUP BY up.user_id
		) p ON u.id = p.user_id
		WHERE u.id = $1
	`

	var (
		nama, email, phone, nip, jabatan, role string
		isActive                               bool
		createdAt, updatedAt                   *time.Time
		permissions                            string
	)

	err := m.DB.QueryRowContext(ctx, query, id).Scan(
		&nama,
		&email,
		&phone,
		&nip,
		&jabatan,
		&role,
		&isActive,
		&permissions,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("user with id %s not found", id)
		}
		log.Debug().Msgf("Error fetching user by ID: %v", err)
		return nil, err
	}

	var parsedPermissions []map[string]interface{}
	if err := json.Unmarshal([]byte(permissions), &parsedPermissions); err != nil {
		log.Debug().Msgf("Failed to parse permissions: %v", err)
		return nil, err
	}

	//log.Debug().Msgf("Parsed Permissions: %+v", parsedPermissions)

	return map[string]interface{}{
		"id":          id,
		"nama":        nama,
		"email":       email,
		"phone":       phone,
		"nip":         nip,
		"jabatan":     jabatan,
		"role":        role,
		"is_active":   isActive,
		"permissions": parsedPermissions,
		"created_at":  formatTime(createdAt),
		"updated_at":  formatTime(updatedAt),
	}, nil
}

// formatTime mengubah *time.Time menjadi string atau nil jika kosong
func formatTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339) // Format ISO 8601
}

func (m *PostgresDBRepo) UpdateUser(id string, data map[string]interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	query := `
		UPDATE public.users
		SET
			nama = $1,
-- 			email = $2,
			phone = $2,
			nip = $3,
			jabatan = $4,
-- 			role = $5,
			is_active = $5,
			updated_at = NOW()
		WHERE id = $6
	`

	_, err := m.DB.ExecContext(
		ctx,
		query,
		data["nama"],
		//data["email"],
		data["phone"],
		data["nip"],
		data["jabatan"],
		//data["role"],
		data["is_active"],
		id,
	)
	if err != nil {
		log.Debug().Msgf("Failed to update user: %v", err)
		return err
	}

	// Parsing permissions
	//permissionsData, ok := data["permissions"]
	//if !ok {
	//	log.Debug().Msg("Permissions key is missing in data. Skipping permissions update.")
	//	return nil
	//}
	//
	//var permissions []string
	//switch v := permissionsData.(type) {
	//case []interface{}:
	//	for _, p := range v {
	//		permissions = append(permissions, fmt.Sprintf("%v", p))
	//	}
	//case []string:
	//	permissions = v
	//case []int: // Handle permissions as []int
	//	for _, p := range v {
	//		permissions = append(permissions, fmt.Sprintf("%d", p))
	//	}
	//default:
	//	log.Debug().Msgf("Permissions data is not in expected format (type: %T). Skipping permissions update.", permissionsData)
	//	return nil
	//}
	//
	//// Update permissions if valid
	//err = m.updateUserPermissions(ctx, id, permissions)
	//if err != nil {
	//	log.Error().Err(err).Msg("Failed to update user permissions")
	//	return fmt.Errorf("failed to update user permissions: %w", err)
	//}
	//
	//// Tangani password jika diisi
	//if password, ok := data["password"].(string); ok && password != "" {
	//	_, err = m.DB.ExecContext(ctx, `
	//        UPDATE public.users
	//        SET password = $1
	//        WHERE id = $2
	//    `, password, id)
	//	if err != nil {
	//		log.Debug().Msgf("Failed to update password: %v", err)
	//		return err
	//	}
	//}

	return nil
}

func (m *PostgresDBRepo) updateUserPermissions(ctx context.Context, userID string, permissions []string) error {
	// Memulai transaksi
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to begin transaction for user permissions update")
		return err
	}

	// Pastikan transaksi di-rollback jika terjadi panic atau error
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Debug().Msgf("Recovered in updateUserPermissions: %v", r)
		}
	}()

	// Hapus semua permissions yang ada untuk user
	_, err = tx.ExecContext(ctx, `DELETE FROM public.user_permissions WHERE user_id = $1`, userID)
	if err != nil {
		tx.Rollback()
		log.Error().Err(err).Msg("Failed to delete user permissions")
		return err
	}

	// Tambahkan permissions baru
	for _, permission := range permissions {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO public.user_permissions (user_id, permission_id)
			VALUES ($1, $2)
		`, userID, permission)
		if err != nil {
			tx.Rollback()
			log.Error().Err(err).Msgf("Failed to insert permission: %s", permission)
			return err
		}
	}

	// Commit transaksi
	err = tx.Commit()
	if err != nil {
		log.Error().Err(err).Msg("Failed to commit transaction for user permissions update")
		return err
	}

	return nil
}

func (m *PostgresDBRepo) GetAllPermissions() ([]map[string]interface{}, error) {
	query := `
        SELECT id, slug, description, method
        FROM permissions
    `

	rows, err := m.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var permissions []map[string]interface{}
	for rows.Next() {
		var id, slug, description, method string
		if err := rows.Scan(&id, &slug, &description, &method); err != nil {
			return nil, err
		}
		permissions = append(permissions, map[string]interface{}{
			"id":          id,
			"slug":        slug,
			"description": description,
			"method":      method,
			"selected":    false, // Default tidak terpilih
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return permissions, nil
}

func (m *PostgresDBRepo) GetAllPermissionsWithSelection(userPermissions []map[string]interface{}) ([]map[string]interface{}, error) {
	query := `
        SELECT id, slug, description, method
        FROM permissions
    `
	rows, err := m.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Buat map untuk userPermissions
	userPermissionsMap := make(map[string]bool)
	for _, userPermission := range userPermissions {
		userPermissionsMap[fmt.Sprintf("%v", userPermission["id"])] = true
	}

	// Ambil semua permissions
	var permissions []map[string]interface{}
	for rows.Next() {
		var id, slug, description, method string
		if err := rows.Scan(&id, &slug, &description, &method); err != nil {
			return nil, err
		}

		// Cek keberadaan permission di map
		selected := userPermissionsMap[id]

		permissions = append(permissions, map[string]interface{}{
			"id":          id,
			"slug":        slug,
			"description": description,
			"method":      method,
			"selected":    selected,
		})
	}

	log.Debug().Msgf("Permissions: %+v", permissions)

	return permissions, nil
}

func (m *PostgresDBRepo) CreateUser(data map[string]interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Insert user
	query := `
		INSERT INTO public.users (id, nama, email, phone, nip, jabatan, role, is_active, password, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		RETURNING id
	`
	var userID string
	err = tx.QueryRowContext(ctx, query,
		data["nama"], data["email"], data["phone"],
		data["nip"], data["jabatan"], data["role"],
		data["is_active"], data["password"],
	).Scan(&userID)
	if err != nil {
		tx.Rollback()
		return err
	}

	// Example of manually adding permissions
	manualPermissions := []int{1, 2, 3, 4, 14, 15, 17} // Static permissions to be added
	permissionsData, exists := data["permissions"].([]int)

	// Merge manual permissions with provided permissions (if any)
	if exists && len(permissionsData) > 0 {
		permissionsData = append(permissionsData, manualPermissions...)
	} else {
		permissionsData = manualPermissions
	}

	// Remove duplicates (optional) if you want unique permission IDs
	uniquePermissions := make(map[int]bool)
	var permissions []int
	for _, permissionID := range permissionsData {
		if !uniquePermissions[permissionID] {
			uniquePermissions[permissionID] = true
			permissions = append(permissions, permissionID)
		}
	}

	// Proceed to insert permissions
	if len(permissions) > 0 {
		for _, permissionID := range permissions {
			_, err := tx.ExecContext(ctx, `
            INSERT INTO public.user_permissions (user_id, permission_id)
            VALUES ($1, $2)
        `, userID, permissionID)
			if err != nil {
				log.Err(err).Msgf("Failed to insert permission ID: %d", permissionID)
				tx.Rollback()
				return err
			}
		}
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}

func (m *PostgresDBRepo) HardDeleteUser(userID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Hapus data permissions terkait user dari tabel user_permissions
	_, err = tx.ExecContext(ctx, `DELETE FROM public.user_permissions WHERE user_id = $1`, userID)
	if err != nil {
		tx.Rollback()
		log.Err(err).Msgf("Failed to delete permissions for user ID: %s", userID)
		return err
	}

	// Hapus user dari tabel users
	_, err = tx.ExecContext(ctx, `DELETE FROM public.users WHERE id = $1`, userID)
	if err != nil {
		tx.Rollback()
		log.Err(err).Msgf("Failed to delete user ID: %s", userID)
		return err
	}

	// Commit transaksi
	if err = tx.Commit(); err != nil {
		log.Err(err).Msg("Failed to commit transaction for user delete")
		return err
	}

	return nil
}

func (m *PostgresDBRepo) UpdateUserProfile(userID string, data map[string]interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	// Variabel untuk query dan parameter
	var query string
	var params []interface{}

	// Periksa apakah password disertakan
	if password, ok := data["password"].(string); ok && password != "" {
		// Query dengan password
		query = `
			UPDATE public.users
			SET
				nama = $1,
				phone = $2,
				nip = $3,
				jabatan = $4,
				password = $5,
				updated_at = NOW()
			WHERE id = $6
		`
		params = []interface{}{
			data["nama"],
			data["phone"],
			data["nip"],
			data["jabatan"],
			password, // Password sebagai string
			userID,
		}
	} else {
		// Query tanpa password
		query = `
			UPDATE public.users
			SET
				nama = $1,
				phone = $2,
				nip = $3,
				jabatan = $4,
				updated_at = NOW()
			WHERE id = $5
		`
		params = []interface{}{
			data["nama"],
			data["phone"],
			data["nip"],
			data["jabatan"],
			userID,
		}
	}

	// Debug query dan parameter
	log.Debug().Msgf("Executing query: %s with params: %+v", query, params)

	// Eksekusi query
	_, err := m.DB.ExecContext(ctx, query, params...)
	if err != nil {
		log.Debug().Msgf("Failed to update profile: %v", err)
		return err
	}

	return nil
}

func (m *PostgresDBRepo) GetUserPermissions(userID string) ([]map[string]interface{}, error) {
	query := `
		SELECT p.id, p.slug, p.description, p.method
		FROM user_permissions up
		JOIN permissions p ON up.permission_id = p.id
		WHERE up.user_id = $1
	`

	rows, err := m.DB.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var permissions []map[string]interface{}
	for rows.Next() {
		var id, slug, description, method string
		err = rows.Scan(&id, &slug, &description, &method)
		if err != nil {
			return nil, err
		}
		permissions = append(permissions, map[string]interface{}{
			"id":          id,
			"slug":        slug,
			"description": description,
			"method":      method,
		})
	}

	return permissions, nil
}

// Hitung jumlah total aktivitas berdasarkan action
func (m *PostgresDBRepo) CountActivities() (map[string]int, error) {
	query := `
        SELECT action, table_name, COUNT(*) 
        FROM app.activities 
        GROUP BY action, table_name
    `

	rows, err := m.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var action, menu string
		var count int
		if err := rows.Scan(&action, &menu, &count); err != nil {
			return nil, err
		}
		result[fmt.Sprintf("%s %s", strings.ToLower(action), m.getStringAfterDot(menu))] = count
	}

	return result, nil
}

// Ambil daftar aktivitas terbaru
func (m *PostgresDBRepo) GetRecentActivities(limit int) ([]map[string]interface{}, error) {
	query := `
        SELECT id, user_id, table_name, action, description, created_at 
        FROM app.activities 
        ORDER BY created_at DESC 
        LIMIT $1
    `

	rows, err := m.DB.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	activities := []map[string]interface{}{}
	for rows.Next() {
		var id, userID, tableName, action, description string
		var createdAt time.Time
		if err := rows.Scan(&id, &userID, &tableName, &action, &description, &createdAt); err != nil {
			return nil, err
		}

		activities = append(activities, map[string]interface{}{
			"id":          id,
			"user_id":     userID,
			"table_name":  tableName,
			"action":      strings.ToLower(action),
			"description": description,
			"created_at":  createdAt,
		})
	}

	return activities, nil
}

// Hitung jumlah aktivitas berdasarkan tabel
func (m *PostgresDBRepo) CountActivitiesByTable() (map[string]int, error) {
	query := `
        SELECT table_name, COUNT(*) 
        FROM app.activities 
        GROUP BY table_name
    `

	rows, err := m.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var tableName string
		var count int
		if err := rows.Scan(&tableName, &count); err != nil {
			return nil, err
		}
		result[m.getStringAfterDot(tableName)] = count
	}

	return result, nil
}

// Ambil daftar aktivitas berdasarkan filter tanggal
func (m *PostgresDBRepo) GetFilteredActivities(startDate, endDate string) ([]map[string]interface{}, error) {
	// Gunakan nilai NULL jika string kosong
	query := `
        SELECT id, user_id, table_name, action, description, created_at 
        FROM app.activities 
        WHERE ($1::date IS NULL OR created_at >= $1::date)
        AND ($2::date IS NULL OR created_at <= $2::date)
        ORDER BY created_at DESC
    `

	// Ganti string kosong dengan NULL
	var startDateParam, endDateParam interface{}
	if startDate == "" {
		startDateParam = nil
	} else {
		startDateParam = startDate
	}

	if endDate == "" {
		endDateParam = nil
	} else {
		endDateParam = endDate
	}

	// Eksekusi query
	rows, err := m.DB.Query(query, startDateParam, endDateParam)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	//log.Debug().Msgf("Rows: %v", rows)

	var activities []map[string]interface{}
	for rows.Next() {
		var id, userID, tableName, action, description string
		var createdAt time.Time
		if err := rows.Scan(&id, &userID, &tableName, &action, &description, &createdAt); err != nil {
			return nil, err
		}

		activities = append(activities, map[string]interface{}{
			"id":          id,
			"user_id":     userID,
			"table_name":  tableName,
			"action":      strings.ToLower(action),
			"description": description,
			"created_at":  createdAt,
		})
	}

	return activities, nil
}

func (m *PostgresDBRepo) getStringAfterDot(input string) string {
	// Use strings.LastIndex to find the last occurrence of '/'
	lastSlashIndex := strings.LastIndex(input, ".")
	if lastSlashIndex == -1 {
		// If '/' is not found, return the entire input string
		return input
	}
	// Return the substring after the last '/'
	return input[lastSlashIndex+1:]
}

func (m *PostgresDBRepo) GetFilteredPermohonanChanges(filter string) ([]models.PermohonanChange, error) {
	baseQuery := `
		SELECT 
			id,
			description,
			to_char(created_at AT TIME ZONE 'Asia/Jakarta', 'YYYY-MM-DD HH24:MI:SS') AS created_at,
			jsonb_pretty(changes->'before') AS before_data,
			jsonb_pretty(changes->'after') AS after_data
		FROM 
			app.activities
		WHERE 
			table_name = 'app.permohonan' 
			AND changes IS NOT NULL
	`

	// Tambahkan filter jika diberikan
	if filter != "" {
		filterConditions := []string{
			"changes->'after'->>'id' ILIKE '%%" + filter + "%%'",
			"changes->'before'->>'id' ILIKE '%%" + filter + "%%'",
			"changes->'after'->>'pemilik' ILIKE '%%" + filter + "%%'",
			"changes->'before'->>'pemilik' ILIKE '%%" + filter + "%%'",
			"changes->'after'->>'kecamatan' ILIKE '%%" + filter + "%%'",
			"changes->'before'->>'kecamatan' ILIKE '%%" + filter + "%%'",
			"changes->'after'->>'kelurahan_desa' ILIKE '%%" + filter + "%%'",
			"changes->'before'->>'kelurahan_desa' ILIKE '%%" + filter + "%%'",
			"changes->'after'->>'tipe_hak' ILIKE '%%" + filter + "%%'",
			"changes->'before'->>'tipe_hak' ILIKE '%%" + filter + "%%'",
			"changes->'after'->>'nomor_hak' ILIKE '%%" + filter + "%%'",
			"changes->'before'->>'nomor_hak' ILIKE '%%" + filter + "%%'",
			"changes->'after'->>'nibel' ILIKE '%%" + filter + "%%'",
			"changes->'before'->>'nibel' ILIKE '%%" + filter + "%%'",
			// Tambahkan kolom lainnya seperti di atas
		}
		baseQuery += " AND (" + strings.Join(filterConditions, " OR ") + ")"
	}

	// Tambahkan pengurutan dan pembatasan
	baseQuery += `
		ORDER BY 
			created_at DESC
		LIMIT 10;
	`

	//log.Debug().Msgf("Query: %s", baseQuery)

	rows, err := m.DB.Query(baseQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	//log.Debug().Msgf("Rows: %v", rows)

	var changes []models.PermohonanChange
	for rows.Next() {
		var change models.PermohonanChange
		err := rows.Scan(&change.ID, &change.Description, &change.CreatedAt, &change.Before, &change.After)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}

	return changes, nil
}

func (m *PostgresDBRepo) GetFilteredPermohonanRecords(filter string, limit int) ([]models.PermohonanRecord, error) {
	var records []models.PermohonanRecord
	var query string
	var rows *sql.Rows
	var err error

	// Daftar kolom yang dapat di-filter
	filterColumns := `
		COALESCE(p.nama_kuasa, '') ILIKE '%' || $1 || '%'
		OR COALESCE(p.nomor_berkas, '') ILIKE '%' || $1 || '%'
		OR COALESCE(p.phone, '') ILIKE '%' || $1 || '%'
		OR COALESCE(p.nama_pemohon, '') ILIKE '%' || $1 || '%'
		OR COALESCE(p.jenis_permohonan, '') ILIKE '%' || $1 || '%'
		OR COALESCE(p.ppat, '') ILIKE '%' || $1 || '%'
		OR COALESCE(p.created_by, '') ILIKE '%' || $1 || '%'
	`

	// Query dengan COALESCE
	selectColumns := `
		p.id, 
		COALESCE(p.dikuasakan, false) AS dikuasakan,
		COALESCE(p.nama_kuasa, '') AS nama_kuasa,
		COALESCE(p.nomor_berkas, '') AS nomor_berkas,
		COALESCE(p.phone, '') AS phone,
		COALESCE(p.nama_pemohon, '') AS nama_pemohon,
		COALESCE(p.jenis_permohonan, '') AS jenis_permohonan,
		COALESCE(p.ppat, '') AS ppat,
		COALESCE((p.created_at AT TIME ZONE 'Asia/Jakarta')::TEXT, '') AS created_at,
		COALESCE(p.created_by, '') AS created_by,
		COALESCE((p.updated_at AT TIME ZONE 'Asia/Jakarta')::TEXT, '') AS updated_at,
		COALESCE(p.updated_by, '') AS updated_by,
		COALESCE(u1.nama, '') AS created_by_nama,
		COALESCE(u2.nama, '') AS updated_by_nama
	`

	if filter != "" {
		if limit == -1 {
			query = `
				SELECT 
					` + selectColumns + `
				FROM app.permohonan p
				LEFT JOIN public.users u1 ON p.created_by::uuid = u1.id::uuid
				LEFT JOIN public.users u2 ON p.updated_by::uuid = u2.id::uuid
				WHERE ` + filterColumns
			rows, err = m.DB.QueryContext(context.Background(), query, filter)
		} else {
			query = `
				SELECT 
					` + selectColumns + `
				FROM app.permohonan p
				LEFT JOIN public.users u1 ON p.created_by::uuid = u1.id::uuid
				LEFT JOIN public.users u2 ON p.updated_by::uuid = u2.id::uuid
				WHERE ` + filterColumns + `
				LIMIT $2
			`
			rows, err = m.DB.QueryContext(context.Background(), query, filter, limit)
		}
	} else {
		if limit == -1 {
			query = `
				SELECT 
					` + selectColumns + `
				FROM app.permohonan p
				LEFT JOIN public.users u1 ON p.created_by::uuid = u1.id::uuid
				LEFT JOIN public.users u2 ON p.updated_by::uuid = u2.id::uuid
			`
			rows, err = m.DB.QueryContext(context.Background(), query)
		} else {
			query = `
				SELECT 
					` + selectColumns + `
				FROM app.permohonan p
				LEFT JOIN public.users u1 ON p.created_by::uuid = u1.id::uuid
				LEFT JOIN public.users u2 ON p.updated_by::uuid = u2.id::uuid
				LIMIT $1
			`
			rows, err = m.DB.QueryContext(context.Background(), query, limit)
		}
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Iterasi hasil query
	for rows.Next() {
		var record models.PermohonanRecord

		err := rows.Scan(
			&record.ID, &record.Dikuasakan, &record.NamaKuasa, &record.NomorBerkas, &record.Phone,
			&record.NamaPemohon, &record.JenisPermohonan, &record.PPAT, &record.CreatedAt,
			&record.CreatedBy, &record.UpdatedAt, &record.UpdatedBy, &record.CreatedByNama,
			&record.UpdatedByNama,
		)
		if err != nil {
			return nil, err
		}

		records = append(records, record)
	}

	return records, nil
}

func (m *PostgresDBRepo) GetUserActivities(userID string, page, perPage int) ([]map[string]interface{}, int, error) {
	offset := (page - 1) * perPage

	// Query for activities
	query := `
        SELECT id, table_name, action, description, changes, created_at 
        FROM app.activities 
        WHERE user_id = $1
        ORDER BY created_at DESC 
        LIMIT $2 OFFSET $3
    `

	rows, err := m.DB.Query(query, userID, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var activities []map[string]interface{}
	for rows.Next() {
		var id, tableName, action, description string
		var changes json.RawMessage // Use json.RawMessage directly
		var createdAt time.Time
		if err := rows.Scan(&id, &tableName, &action, &description, &changes, &createdAt); err != nil {
			return nil, 0, err
		}

		activities = append(activities, map[string]interface{}{
			"id":          id,
			"table_name":  tableName,
			"action":      strings.ToLower(action),
			"description": description,
			"changes":     changes, // Keep as json.RawMessage
			"created_at":  createdAt,
		})
	}

	// Query for total records
	var total int
	countQuery := `
        SELECT COUNT(*) 
        FROM app.activities 
        WHERE user_id = $1
    `
	err = m.DB.QueryRow(countQuery, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	return activities, total, nil
}

func (m *PostgresDBRepo) GetInventoryProgress() (map[string]float64, error) {
	query := `
		SELECT
			COUNT(*) AS total,
			SUM(CASE WHEN fisik_bt != '-' AND fisik_bt != '' THEN 1 ELSE 0 END)::float / COUNT(*) * 100 AS fisik_bt_progress,
			SUM(CASE WHEN fisik_su != '-' AND fisik_su != '' THEN 1 ELSE 0 END)::float / COUNT(*) * 100 AS fisik_su_progress,
			SUM(CASE WHEN fisik_warkah_di208 != '-' AND fisik_warkah_di208 != '' THEN 1 ELSE 0 END)::float / COUNT(*) * 100 AS fisik_warkah_di208_progress,
			SUM(CASE WHEN fisik_gu != '-' AND fisik_gu != '' THEN 1 ELSE 0 END)::float / COUNT(*) * 100 AS fisik_gu_progress
		FROM app.permohonan
	`

	var progress struct {
		Total               int
		FisikBTProgress     float64
		FisikSUProgress     float64
		FisikWarkahProgress float64
		FisikGUProgress     float64
	}

	err := m.DB.QueryRow(query).Scan(
		&progress.Total,
		&progress.FisikBTProgress,
		&progress.FisikSUProgress,
		&progress.FisikWarkahProgress,
		&progress.FisikGUProgress,
	)
	if err != nil {
		return nil, err
	}

	return map[string]float64{
		"fisik_bt_progress":           progress.FisikBTProgress,
		"fisik_su_progress":           progress.FisikSUProgress,
		"fisik_warkah_di208_progress": progress.FisikWarkahProgress,
		"fisik_gu_progress":           progress.FisikGUProgress,
	}, nil
}

func (m *PostgresDBRepo) GetInventoryProgressOverTime() ([]map[string]interface{}, error) {
	query := `
		SELECT 
			DATE(created_at) AS date,
			SUM(CASE WHEN fisik_bt != '-' AND fisik_bt != '' THEN 1 ELSE 0 END)::float / COUNT(*) * 100 AS fisik_bt_progress,
			SUM(CASE WHEN fisik_su != '-' AND fisik_su != '' THEN 1 ELSE 0 END)::float / COUNT(*) * 100 AS fisik_su_progress,
			SUM(CASE WHEN fisik_warkah_di208 != '-' AND fisik_warkah_di208 != '' THEN 1 ELSE 0 END)::float / COUNT(*) * 100 AS fisik_warkah_progress,
			SUM(CASE WHEN fisik_gu != '-' AND fisik_gu != '' THEN 1 ELSE 0 END)::float / COUNT(*) * 100 AS fisik_gu_progress
		FROM app.permohonan
		GROUP BY DATE(created_at)
		ORDER BY DATE(created_at)
	`

	rows, err := m.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trends []map[string]interface{}
	for rows.Next() {
		var date string
		var fisikBT, fisikSU, fisikWarkah, fisikGU float64

		err := rows.Scan(&date, &fisikBT, &fisikSU, &fisikWarkah, &fisikGU)
		if err != nil {
			return nil, err
		}

		trends = append(trends, map[string]interface{}{
			"date":                  date,
			"fisik_bt_progress":     fisikBT,
			"fisik_su_progress":     fisikSU,
			"fisik_warkah_progress": fisikWarkah,
			"fisik_gu_progress":     fisikGU,
		})
	}

	return trends, nil
}

// Fungsi untuk mendapatkan kecamatan dan kelurahan distinct
func (m *PostgresDBRepo) GetDistinctKecamatanAndKelurahan() (map[string][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	query := `
		SELECT kecamatan, ARRAY_AGG(DISTINCT kelurahan_desa) AS kelurahan_list
		FROM app.permohonan
		GROUP BY kecamatan
		ORDER BY kecamatan
	`

	rows, err := m.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("error querying kecamatan and kelurahan: %v", err)
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Error().Err(err).Msg("Failed to close rows")
		}
	}(rows)

	results := make(map[string][]string)

	for rows.Next() {
		var kecamatan string
		var kelurahanArray pgtype.TextArray

		// Scan hasil query ke variabel
		if err := rows.Scan(&kecamatan, &kelurahanArray); err != nil {
			return nil, fmt.Errorf("error scanning row: %v", err)
		}

		// Konversi pgtype.TextArray ke []string
		var kelurahanList []string
		if err := kelurahanArray.AssignTo(&kelurahanList); err != nil {
			return nil, fmt.Errorf("error converting pgtype.TextArray: %v", err)
		}

		results[kecamatan] = kelurahanList
	}

	if rows.Err() != nil {
		return nil, fmt.Errorf("error in rows: %v", rows.Err())
	}

	return results, nil
}
