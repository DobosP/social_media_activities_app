package web

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"sync"

	"github.com/flosch/pongo2/v6"
)

// These models contain the existing authorized group projection only. No query,
// membership decision, authority union or arbitrary safe HTML is performed here.
type groupDetailPage struct {
	Shell                                                                                        familyShell
	Copy                                                                                         map[string]string
	ID                                                                                           int64
	Title, Description, Kind, Area, CohortLabel, StatusLabel                                     string
	Cohort, Status, Canonical, Nonce, PublicID                                                   string
	JoinURL, LeaveURL, ArchiveURL, ListingURL, AnnounceURL, PostURL, AskURL, RosterHeading       string
	Owner, Member, Listed, ShowThread, CanPost, CanAnnounce, CanAsk, LiveThread, DissentControls bool
	RosterVisible                                                                                bool
	Roster                                                                                       []groupDetailPerson
	Announcements, Posts                                                                         []groupDetailPost
	Feed                                                                                         []groupDetailActivity
	Questions                                                                                    []familyOption
	ReactionFacets                                                                               []groupDetailFacet
	ComposeBody, ComposeReplyTo, ReplyAuthor                                                     string
	ChatJSON, NudgeJSON                                                                          template.JS
}
type groupDetailPerson struct {
	ID                         int64
	Name, PublicID, ProfileURL string
	Avatar                     template.URL
	Other                      bool
}
type groupDetailPost struct {
	Copy                                                                      map[string]string
	ID, AuthorID                                                              int64
	Author                                                                    groupDetailPerson
	When, ISOTime, Aria, Body, CSRF, ConcernIntro                             string
	BodyHTML                                                                  template.HTML
	Lines                                                                     []string
	Mine, Edited, Member, DissentControls, Dissent, Concern                   bool
	ReplyURL, ReactURL, DissentURL, ConcernURL, EditURL, DeleteURL, ReportURL string
	Snippet                                                                   *groupDetailSnippet
	Share                                                                     *groupDetailShare
	Media                                                                     []groupDetailAttachment
	Sentiment                                                                 []string
	Facets                                                                    []groupDetailFacet
	Replies                                                                   []groupDetailPost
}
type groupDetailSnippet struct {
	ID                  int64
	Author, Text, Label string
}
type groupDetailFacet struct {
	Slug, Emoji, Label, Aria string
	Mine                     bool
}
type groupDetailShare struct{ Kind, URL, Title, When, Place string }
type groupDetailAttachment struct {
	Kind, URL, Thumb, Poster, Filename, Expiry string
	Blocked, Processing, Failed, Expired       bool
}
type groupDetailActivity struct {
	Title, URL, Type, Cost, Difficulty, Status, When, Place, City, Description, Reason, Match, Distance string
	Secondary                                                                                           []string
	Beginners, Guardian, Accent                                                                         bool
	BeginnersText, GuardianText                                                                         string
	PhotoURL, PhotoAlt                                                                                  string
	AccentSVG                                                                                           template.HTML
}

//go:embed pages/group-detail*.gohtml
var groupDetailPages embed.FS

var errGroupDetailSnapshot = errors.New("invalid native group detail snapshot")

var loadGroupDetailTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("group-detail").ParseFS(groupDetailPages, "pages/group-detail*.gohtml")
})

func (r *Renderer) renderGroupDetail(request *http.Request, data pongo2.Context) ([]byte, error) {
	page, err := r.groupDetailPage(request, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadGroupDetailTemplates()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.Grow(12288 + len(page.Posts)*3072 + len(page.Roster)*256)
	if err = parsed.ExecuteTemplate(&output, "group-detail", page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
