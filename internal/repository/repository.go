package repository

import (
	"database/sql"
	"monitor-loket/internal/models"
)

// DashboardData struct for the dashboard

type DatabaseRepo interface {
	Connection() *sql.DB
	CreateMessage(data map[string]interface{}) error

	Login(email, password string) (map[string]interface{}, error)
	LoginCheckUserIsActive(email string) (bool, error)
	CreatePermohonan(records []map[string]interface{}) ([]string, error)
	GetAllPermohonan(page, perPage int, sort, order, search string) ([]map[string]interface{}, int, error)
	GetAllPermohonanByUserID(page, perPage int, sort, order, search, userID string) ([]map[string]interface{}, int, error)
	GetPermohonanByID(id string) (map[string]interface{}, error)
	UpdatePermohonanByID(id string, data map[string]interface{}) error
	UpdatePassword(userID, newPassword string) error
	LogActivity(activity map[string]interface{}) error
	HardDeletePermohonan(arsipID string) error
	GetAllUsers(page, perPage int, sort, order, search string) ([]map[string]interface{}, int, error)
	GetAllUsersExceptKakan(page, perPage int, sort, order, search string) ([]map[string]interface{}, int, error)
	GetUserByID(id string) (map[string]interface{}, error)
	UpdateUser(id string, data map[string]interface{}) error
	GetAllPermissions() ([]map[string]interface{}, error)
	GetAllPermissionsWithSelection(userPermissions []map[string]interface{}) ([]map[string]interface{}, error)
	CreateUser(data map[string]interface{}) error
	HardDeleteUser(userID string) error
	UpdateUserProfile(userID string, data map[string]interface{}) error
	GetUserPermissions(userID string) ([]map[string]interface{}, error)
	CountActivities() (map[string]int, error)
	GetRecentActivities(limit int) ([]map[string]interface{}, error)
	CountActivitiesByTable() (map[string]int, error)
	GetFilteredActivities(startDate, endDate string) ([]map[string]interface{}, error)
	GetFilteredPermohonanChanges(filter string) ([]models.PermohonanChange, error)
	GetFilteredPermohonanRecords(filter string, limit int) ([]models.PermohonanRecord, error)
	GetUserActivities(userID string, page, perPage int) ([]map[string]interface{}, int, error)
	GetInventoryProgress() (map[string]float64, error)
	GetInventoryProgressOverTime() ([]map[string]interface{}, error)
	GetDistinctKecamatanAndKelurahan() (map[string][]string, error)
}
