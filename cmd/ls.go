package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/apsdsm/meimei/internal/catalog"
	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List the containers this project can build",
	Long: "List every service declared in " + ".meimei.toml" + ", what it builds from,\n" +
		"and the image a build would produce right now.\n\n" +
		"Reads only the working tree — no docker daemon, no AWS, no credentials.",
	Args: cobra.NoArgs,
	RunE: runLs,
}

func init() {
	lsCmd.Flags().Bool("json", false, "Emit machine-readable JSON")
	lsCmd.Flags().String("label", "", "Resolve tags against a release or ticket label instead of the commit")
	rootCmd.AddCommand(lsCmd)
}

func runLs(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	label, _ := cmd.Flags().GetString("label")
	cat := catalog.Load(cfg, label)

	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		return emitJSON(cat)
	}
	emitText(cat)

	// A broken definition is a failure the caller should be able to detect —
	// this command is the pre-flight for a build, and a script gating on it
	// cannot read the ✕ on screen.
	if _, broken, _ := cat.Counts(); broken > 0 {
		return fmt.Errorf("%d service(s) cannot be built", broken)
	}
	return nil
}

func emitText(cat *catalog.Catalog) {
	buildable, broken, disabled := cat.Counts()

	summary := fmt.Sprintf("%d buildable", buildable)
	if broken > 0 {
		summary += fmt.Sprintf(", %d broken", broken)
	}
	if disabled > 0 {
		summary += fmt.Sprintf(", %d disabled", disabled)
	}
	fmt.Printf("%s  %s  %s\n", cat.Config.Project.Name, cat.Config.Root, summary)

	switch {
	case !cat.Git.Available:
		fmt.Println("no git — a build cannot be tagged by commit")
	case cat.Label != "":
		fmt.Printf("label %s\n", cat.Label)
	case cat.Git.Dirty:
		fmt.Printf("tag %s — working tree is dirty, an image built now matches no commit\n", cat.Git.Tag(""))
	default:
		fmt.Printf("tag %s\n", cat.Git.Tag(""))
	}
	fmt.Println()

	fmt.Print(renderTable(cat))

	for _, e := range cat.Entries {
		if e.Problem != "" {
			fmt.Printf("✕ %s: %s\n", e.Image.Name, e.Problem)
		}
	}
}

// renderTable lays the services out in columns, arranged by group.
//
// Headings and blank separators carry trailing tabs so that tabwriter keeps ONE
// set of column widths for the whole table — without them it treats each
// heading as a column break and restarts, leaving every group aligned to itself
// and the table ragged across groups. The cost is that tabwriter then pads
// those lines out to the full width, so the finished text is trimmed line by
// line on the way out.
func renderTable(cat *catalog.Catalog) string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	for _, group := range cat.Groups() {
		name := group.Name
		if name == "" {
			name = "(ungrouped)"
		}
		fmt.Fprintf(w, "%s\t\t\t\n", name)
		for _, e := range group.Entries {
			fmt.Fprintf(w, "  %s %s\t%s\t%s\t%s\n",
				glyph(e.Status), e.Image.Name, detail(e), image(cat, e), e.Image.Dockerfile)
		}
		fmt.Fprint(w, "\t\t\t\n")
	}
	w.Flush()

	lines := strings.Split(buf.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func glyph(s catalog.Status) string {
	switch s {
	case catalog.Broken:
		return "✕"
	case catalog.Disabled:
		return "○"
	default:
		return "●"
	}
}

// detail is the short "what is this" line: what it is built from and how.
func detail(e catalog.Entry) string {
	if e.Status == catalog.Disabled {
		return "disabled"
	}
	if e.Status == catalog.Broken {
		return "broken"
	}
	parts := []string{e.Info.Base()}
	if n := len(e.Info.Stages); n > 1 {
		parts = append(parts, fmt.Sprintf("%d stages", n))
	}
	for _, p := range e.Info.Expose {
		parts = append(parts, fmt.Sprintf(":%d", p))
	}
	return strings.Join(parts, " · ")
}

// image is repository:tag, or just the repository when there is no tag to show.
func image(cat *catalog.Catalog, e catalog.Entry) string {
	if e.Tag == "" {
		return e.Repository
	}
	return e.Repository + ":" + e.Tag
}

// jsonEntry is the wire shape. The catalog's own types carry config structs and
// would leak the whole file into the output; this is the contract, declared
// separately so it can stay stable while the internals move.
type jsonEntry struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Repository string `json:"repository"`
	Tag        string `json:"tag,omitempty"`
	Image      string `json:"image,omitempty"`
	Group      string `json:"group,omitempty"`
	Dockerfile string `json:"dockerfile"`
	Context    string `json:"context"`
	Platform   string `json:"platform"`
	Base       string `json:"base,omitempty"`
	Stages     int    `json:"stages,omitempty"`
	Expose     []int  `json:"expose,omitempty"`
	Problem    string `json:"problem,omitempty"`
}

type jsonOutput struct {
	Project  string      `json:"project"`
	Root     string      `json:"root"`
	Region   string      `json:"region,omitempty"`
	Tag      string      `json:"tag,omitempty"`
	Label    string      `json:"label,omitempty"`
	Dirty    bool        `json:"dirty"`
	Services []jsonEntry `json:"services"`
}

func emitJSON(cat *catalog.Catalog) error {
	out := jsonOutput{
		Project: cat.Config.Project.Name,
		Root:    cat.Config.Root,
		Region:  cat.Config.Project.Region,
		Tag:     cat.Git.Tag(cat.Label),
		Label:   cat.Label,
		Dirty:   cat.Git.Dirty,
	}
	for _, e := range cat.Entries {
		je := jsonEntry{
			Name:       e.Image.Name,
			Status:     e.Status.String(),
			Repository: e.Repository,
			Tag:        e.Tag,
			Group:      e.Image.Group,
			Dockerfile: e.Image.Dockerfile,
			Context:    e.Image.Context,
			Platform:   e.Image.Platform,
			Base:       e.Info.Base(),
			Stages:     len(e.Info.Stages),
			Expose:     e.Info.Expose,
			Problem:    e.Problem,
		}
		if e.Tag != "" {
			je.Image = e.Repository + ":" + e.Tag
		}
		out.Services = append(out.Services, je)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
