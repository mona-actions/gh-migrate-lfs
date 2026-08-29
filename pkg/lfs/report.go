package lfs

type Stats struct {
	Objects        int `json:"objects"`
	WouldUpload    int `json:"would_upload"`
	Uploaded       int `json:"uploaded"`
	AlreadyPresent int `json:"already_present"`
	ServerErrors   int `json:"server_errors"`
	UploadFailures int `json:"upload_failures"`
	BatchFailures  int `json:"batch_failures"`
	Unexpected     int `json:"unexpected_replies"`
	RemotePresent  int `json:"remote_present"`
	RemoteMissing  int `json:"remote_missing"`
	RemoteErrors   int `json:"remote_errors"`
}

func (s *Stats) Add(other Stats) {
	s.Objects += other.Objects
	s.WouldUpload += other.WouldUpload
	s.Uploaded += other.Uploaded
	s.AlreadyPresent += other.AlreadyPresent
	s.ServerErrors += other.ServerErrors
	s.UploadFailures += other.UploadFailures
	s.BatchFailures += other.BatchFailures
	s.Unexpected += other.Unexpected
	s.RemotePresent += other.RemotePresent
	s.RemoteMissing += other.RemoteMissing
	s.RemoteErrors += other.RemoteErrors
}

type Issue struct {
	OID     string `json:"oid,omitempty"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

type IssueReporter interface {
	ReportIssue(Issue)
}

type IssueReporterFunc func(Issue)

func (reporter IssueReporterFunc) ReportIssue(issue Issue) {
	reporter(issue)
}
