package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// floatID matches an id-like identifier (id, ids, Id, ProjectId, CheckIds,
// announcementId, ...) declared as float32/float64, with optional pointer or
// slice. Ids must be integers in the document (@ApiProperty({type:'integer'})).
var floatID = regexp.MustCompile(`\b(?:ids?|[A-Za-z0-9]*Ids?)\s+\*?(?:\[\])?\*?float(?:32|64)\b`)

func TestGeneratedCodeHasNoFloatIDs(t *testing.T) {
	files, err := filepath.Glob("*.gen.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no generated files found (%v)", err)
	}
	var offenders []string
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if m := floatID.FindString(line); m != "" {
				offenders = append(offenders, file+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(m))
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("%d id fields typed as floats; type them as integers in the OpenAPI document and regenerate:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

func TestFloatIDPattern(t *testing.T) {
	for _, s := range []string{"Id float32", "ProjectId *float32", "CheckIds *[]float32", "id float32", "announcementId float64"} {
		if !floatID.MatchString(s) {
			t.Fatalf("pattern misses %q", s)
		}
	}
	for _, s := range []string{"Id int", "Valid float32", "Paid float64", "Limit *float32", "ProjectId *int"} {
		if floatID.MatchString(s) {
			t.Fatalf("pattern wrongly matches %q", s)
		}
	}
}
