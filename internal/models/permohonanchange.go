package models

type PermohonanChange struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	Before      string `json:"before"`
	After       string `json:"after"`
}
