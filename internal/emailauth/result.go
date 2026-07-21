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
	Status  Status  `json:"status" enum:"pass,fail,none,neutral,softfail,temperror,permerror" doc:"SPF evaluation result. Only pass can contribute to DMARC alignment."`
	Domain  *string `json:"domain" nullable:"true" doc:"RFC 5321 identity domain evaluated by SPF; null when no SPF identity was available."`
	Aligned *bool   `json:"aligned" nullable:"true" doc:"Whether a passing SPF identity aligns with the RFC 5322 Author Domain under the discovered DMARC policy; null unless status is pass and an applicable DMARC record was discovered."`
	Detail  string  `json:"detail,omitempty" doc:"Free-text diagnostic for humans and logs. Never parse or branch on this field."`
}

type DKIMResult struct {
	Status   Status  `json:"status" enum:"pass,fail,none,neutral,policy,temperror,permerror" doc:"Result for this DKIM signature. policy means e2a deliberately refused the signature, such as one using the unsafe l= body-length tag."`
	Domain   *string `json:"domain" nullable:"true" doc:"DKIM signing domain from the signature d= tag; null when it could not be parsed."`
	Selector *string `json:"selector" nullable:"true" doc:"DKIM selector from the signature s= tag; null when it could not be parsed."`
	Aligned  *bool   `json:"aligned" nullable:"true" doc:"Whether a passing DKIM signing domain aligns with the RFC 5322 Author Domain under the discovered DMARC policy; null unless status is pass and an applicable DMARC record was discovered."`
	Detail   string  `json:"detail,omitempty" doc:"Free-text diagnostic for humans and logs. Never parse or branch on this field."`
}

type DMARCResult struct {
	Status Status  `json:"status" enum:"pass,fail,none,temperror,permerror" doc:"DMARC verdict. Only pass authenticates domain-authorized use of the RFC 5322 Author Domain. none means the sender publishes no DMARC record (common, not itself suspicious) and is distinct from fail, an actual alignment mismatch."`
	Domain *string `json:"domain" nullable:"true" doc:"RFC 5322 Author Domain evaluated by DMARC; null when no single valid Author Domain exists."`
	// Closed response enum: exhaustive DMARC policy classification.
	Policy *DMARCPolicy `json:"policy" nullable:"true" enum:"none,quarantine,reject" doc:"Effective policy requested by the applicable DMARC record. This is sender-published metadata, not an action e2a took and not authentication strength."`
	// Closed response item enum: DMARC alignment has exactly two mechanisms.
	AlignedBy []AlignmentMechanism `json:"aligned_by" nullable:"false" enum:"spf,dkim" doc:"Mechanisms that both passed and aligned. Empty unless status is pass; each mechanism appears at most once."`
	Detail    string               `json:"detail,omitempty" doc:"Free-text diagnostic for humans and logs. Never parse or branch on this field."`
}

type Authentication struct {
	SPF   SPFResult    `json:"spf" doc:"SPF evidence for the SMTP peer and RFC 5321 identity."`
	DKIM  []DKIMResult `json:"dkim" nullable:"false" doc:"One result per DKIM signature, in message header order. An unsigned message carries an empty array."`
	DMARC DMARCResult  `json:"dmarc" doc:"DMARC evaluation of the RFC 5322 Author Domain against aligned SPF and DKIM evidence."`
}

func (a *Authentication) Passed() bool {
	return a != nil && a.DMARC.Status == StatusPass
}

// VerifiedDomain returns the RFC 5322 Author Domain only when DMARC passed.
// It is a convenience decision projection; DMARC still authenticates a domain,
// never a mailbox local part, individual human, or message content.
func (a *Authentication) VerifiedDomain() *string {
	if !a.Passed() || a.DMARC.Domain == nil || *a.DMARC.Domain == "" {
		return nil
	}
	domain := *a.DMARC.Domain
	return &domain
}
