package config

const (
	// ApplicationName is the name of the application
	ApplicationName = "Monitor Loket"
	// ApplicationVersion is the version of the application
	ApplicationVersion = "1.0.0"
	// ApplicationAuthor is the author of the application
	ApplicationAuthor = "Reza Yogaswara // reza.yoga@gmail.com // 6282228223500"
	// ApplicationPort is the port of the application
	ApplicationPort  = "8081"
	ApplicationOwner = "Kantor Pertanahan Kota Surabaya II"
	// ApplicationCookieName is the name of the cookie
	// ApplicationCookieName = "__Host-sidita" // must use __Host- prefix for https only
	ApplicationCookieName  = "monitor-loket"
	MetaPhoneNumberID      = "375845648953453"
	MetaWebhookVerifyToken = "2lNjcZNblLYcT8dkw7jFZwE2xfC" // ksuid
	MetaWebhookToken       = "EAAIwJHeYc1QBAGYpB3Nw0vZCrgghQfP8fdsujUUX3Y727NkW4389p3RkZBhY7a1hhWFnLe3Fn0vyd7wNbalxKpnJ2ira03ggvF0ZCzDWyxweJlJhDypFcl1UzVRIZAFOX7fWFsVQ4CUSaUY6PiHZC7UMh2t3mviZBR6LDEnBbZACOfjv4vUuO5OvbVBqZCrILlaZCeTCYDZA1D4QZDZD"

	MetaGraphAPI = "https://graph.facebook.com/v16.0/"

	TemplateSCPanggilanMelengkapiBerkas = "sc_panggilan_melengkapi_berkas"
	TemplateSCSuratPanggilan            = "sc_surat_panggilan"
	TemplateSCJadwalUkur                = "sc_jadwal_ukur"
	TemplateSCJadwalPanitia             = "sc_jadwal_panitia"
	TemplateSCUndanganKehadiran         = "sc_undangan_ke_kantor"
	Passphrase                          = "xbcde0ghijklmnop7rstuv8xyz912345"

	ContextKeyPhoneNumberID    = "x"
	ContextKeyClientIdentifier = "y"

	DateForPasswordChange = 3
	PasswordEntropy       = 70
	OTPExpiry             = 60 // in seconds
	OTPSecretSize         = 128

	KeyBaseURL             = "KeyBaseURL"
	KeyApplicationName     = "KeyApplicationName"
	KeyApplicationOwner    = "KeyApplicationOwner"
	KeyApplicationVersion  = "KeyApplicationVersion"
	KeyApplicationAuthor   = "KeyApplicationAuthor"
	KeySessionAdminNama    = "KeySessionAdminNama"
	KeySessionAdminEmail   = "KeySessionAdminEmail"
	KeySessionAdminRole    = "KeySessionAdminRole"
	KeySessionAdminID      = "KeySessionAdminID"
	KeySessionAdminPhone   = "KeySessionAdminPhone"
	KeySessionAdminJabatan = "KeySessionAdminJabatan"
	KeyNotificationMessage = "KeyNotificationMessage"
	KeyListPermohonan      = "KeyListPermohonan"
	KeyListUser            = "KeyListUser"
	KeyPermohonan          = "KeyPermohonan"
	KeyUser                = "KeyUser"
	KeyListPermision       = "KeyListPermision"
	KeyMessage             = "KeyMessage"
	KeyStatus              = "KeyStatus"
	KeyID                  = "KeyID"
	KeyKe                  = "KeyKe"
	SessionName            = "monitor-loket-session"
	KeyCSRFToken           = "KeyCSRFToken"
	SystemUserID           = "8d0a591c-be6d-4afa-9e7d-445bb3a0cce6"

	FilePanduanAplikasi = "panduan-monitor-loket-v.1.0.0.pdf"
	RoleSuperadmin      = "Superadmin"
	RoleAdmin           = "Admin"
)
