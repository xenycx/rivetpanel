package domain

// Knowledgebase article states and visibilities.
const (
	KBDraft     = "draft"     // only knowledgebase managers see it
	KBPublished = "published" // readers allowed by its visibility see it

	KBPublic = "public" // everyone signed in; anonymous visitors too when the public help center is on
	KBUsers  = "users"  // every signed-in account
	KBStaff  = "staff"  // support staff and knowledgebase managers only
)

// KBStatuses and KBVisibilities are the accepted values.
var (
	KBStatuses     = []string{KBDraft, KBPublished}
	KBVisibilities = []string{KBPublic, KBUsers, KBStaff}
)

// KBCategory groups articles.
type KBCategory struct {
	ID          string
	Slug        string
	Name        string
	Description string
	Position    int
	CreatedAtMS int64
	UpdatedAtMS int64
}

// KBArticle is a help article. Body is Markdown; it is stored and served as
// text and rendered by the interface without interpreting HTML.
type KBArticle struct {
	ID            string
	CategoryID    *string
	Slug          string
	Title         string
	Summary       string
	Body          string // empty in listings
	Status        string
	Visibility    string
	Position      int
	AuthorID      *string
	UpdatedBy     string // label of the last editor
	CreatedAtMS   int64
	UpdatedAtMS   int64
	PublishedAtMS *int64
}

// KBFilter selects articles. Empty slices mean "any".
type KBFilter struct {
	Statuses     []string
	Visibilities []string
	// Terms match title, summary or body (any term, case-insensitive for
	// ASCII); WithBody loads the body.
	Terms    []string
	WithBody bool
	Limit    int
}
