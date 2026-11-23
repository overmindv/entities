package domain

type LogoBindingIntent struct {
	UniversityID string
	FileID       *string
	ActorID      string
	Version      int64
	ClaimToken   string
	Attempts     int
}

type LogoBindingState struct {
	Version int64
	FileID  *string
}
