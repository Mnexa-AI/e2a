package emailauth

// Status is an email-authentication result. Individual mechanisms expose
// only the subset of these values registered for that mechanism.
type Status string

const (
	StatusPass      Status = "pass"
	StatusFail      Status = "fail"
	StatusNone      Status = "none"
	StatusNeutral   Status = "neutral"
	StatusSoftFail  Status = "softfail"
	StatusPolicy    Status = "policy"
	StatusTempError Status = "temperror"
	StatusPermError Status = "permerror"
)

type AlignmentMechanism string

const (
	AlignedBySPF  AlignmentMechanism = "spf"
	AlignedByDKIM AlignmentMechanism = "dkim"
)

type DMARCPolicy string

const (
	DMARCPolicyNone       DMARCPolicy = "none"
	DMARCPolicyQuarantine DMARCPolicy = "quarantine"
	DMARCPolicyReject     DMARCPolicy = "reject"
)

type SPFResult struct {
	Status  Status  `json:"status" enum:"pass,fail,none,neutral,softfail,policy,temperror,permerror"`
	Domain  *string `json:"domain" nullable:"true"`
	Aligned *bool   `json:"aligned" nullable:"true"`
	Detail  string  `json:"detail,omitempty"`
}

type DKIMResult struct {
	Status   Status  `json:"status" enum:"pass,fail,none,neutral,policy,temperror,permerror"`
	Domain   *string `json:"domain" nullable:"true"`
	Selector *string `json:"selector" nullable:"true"`
	Aligned  *bool   `json:"aligned" nullable:"true"`
	Detail   string  `json:"detail,omitempty"`
}

type DMARCResult struct {
	Status    Status               `json:"status" enum:"pass,fail,none,temperror,permerror"`
	Domain    *string              `json:"domain" nullable:"true"`
	Policy    *DMARCPolicy         `json:"policy" nullable:"true"`
	AlignedBy []AlignmentMechanism `json:"aligned_by" nullable:"false"`
	Detail    string               `json:"detail,omitempty"`
}

type Authentication struct {
	SPF   SPFResult    `json:"spf"`
	DKIM  []DKIMResult `json:"dkim" nullable:"false"`
	DMARC DMARCResult  `json:"dmarc"`
}

func (a *Authentication) Passed() bool {
	return a != nil && a.DMARC.Status == StatusPass
}
