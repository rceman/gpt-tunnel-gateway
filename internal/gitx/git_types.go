package gitx

type Runner struct {
	MaxReadBytes int64
	MaxDiffBytes int64
	MaxListItems int
}

type Ref struct {
	Name          string `json:"name"`
	ObjectType    string `json:"object_type"`
	ObjectName    string `json:"object_name"`
	Subject       string `json:"subject,omitempty"`
	CommitterDate string `json:"committer_date,omitempty"`
}
type Commit struct {
	SHA         string   `json:"sha"`
	Parents     []string `json:"parents"`
	AuthorName  string   `json:"author_name"`
	AuthorEmail string   `json:"author_email"`
	AuthorDate  string   `json:"author_date"`
	Subject     string   `json:"subject"`
}
type WorktreeStatus struct {
	Branch    string `json:"branch"`
	Head      string `json:"head"`
	Upstream  string `json:"upstream,omitempty"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Porcelain string `json:"porcelain"`
	Clean     bool   `json:"clean"`
}
type Compare struct {
	MergeBase string `json:"merge_base"`
	LeftOnly  int    `json:"left_only"`
	RightOnly int    `json:"right_only"`
}

type MirrorVerification struct {
	Path          string
	RepositoryURL string
	Head          string
	Created       bool
}
