package catalog

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Dockerfile is the little meimei reads out of a Dockerfile: enough to say on
// screen what a service is, without pretending to understand the build.
//
// This is a reader, not a parser — it recognises FROM and EXPOSE and ignores
// everything else. Docker's own parser is the authority on what a Dockerfile
// means, and duplicating it to fill in a subtitle would be a liability. Any
// line it cannot make sense of is skipped, never an error: a Dockerfile meimei
// cannot summarise is still a Dockerfile docker can build.
type Dockerfile struct {
	Stages []Stage
	Expose []int
}

// Stage is one FROM.
type Stage struct {
	// Image is the base, as written: "golang:1.25", "oven/bun:1".
	Image string
	// As is the stage name from `AS <name>`, empty when unnamed.
	As string
}

// Base is the family name of the first stage's image, with the registry path
// and tag stripped: "golang:1.25" -> "golang", "oven/bun:1" -> "bun".
//
// The FIRST stage, not the last, because that is the one that says what the
// service is written in. The final stage is nearly always the same handful of
// slim runtimes (alpine, distroless) and so tells you nothing that
// distinguishes one service from another.
func (d Dockerfile) Base() string {
	if len(d.Stages) == 0 {
		return ""
	}
	img := d.Stages[0].Image
	if i := strings.IndexAny(img, ":@"); i >= 0 {
		img = img[:i]
	}
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	return img
}

// ReadDockerfile reads and summarises a Dockerfile.
func ReadDockerfile(path string) (Dockerfile, error) {
	f, err := os.Open(path)
	if err != nil {
		return Dockerfile{}, err
	}
	defer f.Close()

	var df Dockerfile
	sc := bufio.NewScanner(f)
	// Dockerfile lines are short, but a long RUN with line continuations can
	// run past bufio's 64KB default, and hitting that would silently truncate
	// the rest of the file — losing stages we would have reported.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "FROM":
			df.Stages = append(df.Stages, parseFrom(fields[1:]))
		case "EXPOSE":
			for _, p := range fields[1:] {
				// "8080/tcp" is legal; the protocol is not interesting here.
				if i := strings.Index(p, "/"); i >= 0 {
					p = p[:i]
				}
				if n, err := strconv.Atoi(p); err == nil {
					df.Expose = append(df.Expose, n)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Dockerfile{}, err
	}
	return df, nil
}

// parseFrom reads the arguments of a FROM line. Flags such as
// `--platform=$BUILDPLATFORM` come before the image and are skipped.
func parseFrom(args []string) Stage {
	var st Stage
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			continue
		}
		if st.Image == "" {
			st.Image = a
			continue
		}
		if strings.EqualFold(a, "AS") && i+1 < len(args) {
			st.As = args[i+1]
			return st
		}
	}
	return st
}
