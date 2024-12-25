package models

type PermohonanRecord struct {
	ID              string `json:"id"`
	Dikuasakan      bool   `json:"dikuasakan"`
	NamaKuasa       string `json:"nama_kuasa"`
	NomorBerkas     string `json:"nomor_berkas"`
	Phone           string `json:"phone"`
	NamaPemohon     string `json:"nama_pemohon"`
	JenisPermohonan string `json:"jenis_permohonan"`
	PPAT            string `json:"ppat"`
	CreatedAt       string `json:"created_at"`
	CreatedBy       string `json:"created_by"`
	UpdatedAt       string `json:"updated_at"`
	UpdatedBy       string `json:"updated_by"`
	CreatedByNama   string `json:"created_by_nama"`
	UpdatedByNama   string `json:"updated_by_nama"`
}
