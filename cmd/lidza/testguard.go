package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// EnvAllowTestChanges confirms a deliberate change that the test guard
// of `lidza verify` would refuse: LIDZA_ALLOW_TEST_CHANGES=1 git commit.
const EnvAllowTestChanges = "LIDZA_ALLOW_TEST_CHANGES"

// allowSkipMarker on a skip's line (or the comment line just above it),
// followed by a reason, lets that one skip through.
const allowSkipMarker = "lidza:allow-skip"

var (
	jsTestFile = regexp.MustCompile(`\.(test|spec)\.[cm]?[jt]sx?$`)
	goSkip     = regexp.MustCompile(`\.(Skip|Skipf|SkipNow)\(`)
	jsSkip     = regexp.MustCompile(`\b(test|it|describe|suite)(\.describe)?\.(skip|only|fixme)\b|\b(xit|xtest|xdescribe|fit|fdescribe)\(`)
	goAssert   = regexp.MustCompile(`\.(Error|Errorf|Fatal|Fatalf)\([^)]|\.(Fail|FailNow)\(\)|\b(assert|require)\.\w+\(`)
	jsAssert   = regexp.MustCompile(`\bexpect(\.soft|\.poll)?\(|\bassert(\.\w+)?\(|\.should\(`)
	allowSkip  = regexp.MustCompile(regexp.QuoteMeta(allowSkipMarker) + `\s+\S`)
	hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
)

// testFinding is one change the test guard refuses.
type testFinding struct {
	File string
	// Line is the line in the staged file, or in HEAD when Old is set;
	// 0 for a whole file.
	Line    int
	Old     bool
	Message string
}

func (f testFinding) String() string {
	switch {
	case f.Line == 0:
		return fmt.Sprintf("%s: %s", f.File, f.Message)
	case f.Old:
		return fmt.Sprintf("%s:%d (HEAD): %s", f.File, f.Line, f.Message)
	default:
		return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Message)
	}
}

// isTestFile reports whether a project-relative path is a Go or a
// JavaScript/TypeScript test.
func isTestFile(path string) bool {
	if strings.HasPrefix(path, "node_modules/") || strings.Contains(path, "/node_modules/") {
		return false
	}
	return strings.HasSuffix(path, "_test.go") || jsTestFile.MatchString(path)
}

// isComment reports a line that is only a comment: a commented-out
// assertion is a removed one.
func isComment(line string) bool {
	s := strings.TrimSpace(line)
	return strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/*") || strings.HasPrefix(s, "*")
}

// allowTestChanges reports the environment's confirmation.
func allowTestChanges() bool {
	ok, _ := strconv.ParseBool(os.Getenv(EnvAllowTestChanges))
	return ok
}

// weakenedTests compares the staged changes with HEAD and lists the ones
// that weaken the tests: a deleted test file, an added skip or only, an
// assertion removed without one added in the same file.
func weakenedTests(ctx context.Context, dir string) ([]testFinding, string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, "git not installed", errSkipped
	}
	head := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "-q", "HEAD")
	head.Dir = dir
	if err := head.Run(); err != nil {
		inside := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
		inside.Dir = dir
		if inside.Run() != nil {
			return nil, "not a git repository", errSkipped
		}
		return nil, "first commit", errSkipped
	}
	// Fixed options, whatever the user's git configuration: renames
	// detected (a moved test is not a deleted one), no context, plain
	// a/ b/ prefixes, paths as seen from the project.
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotePath=false", "diff", "--cached", "--no-color", "--no-ext-diff", "--no-textconv",
		"-M", "-U0", "--src-prefix=a/", "--dst-prefix=b/", "--relative", "HEAD", "--")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("git diff --cached: %w", err)
	}
	findings, notes := parseTestDiffNotes(out)
	return findings, strings.Join(notes, "; "), nil
}

// fileDiff collects one file's changes while the diff is read.
type fileDiff struct {
	path            string
	deleted         bool
	removedSkips    map[string]int
	addedSkips      []testFinding
	addedSkipText   []string
	removedAsserts  []testFinding
	addedAssertions int
	// The assertion lines themselves, to pair a removed one with an
	// added one: the same check with other expected text is a copy
	// update; the same check with a looser matcher weakens it.
	removedAssertText []string
	addedAssert       []assertLine
}

type assertLine struct {
	line int
	text string
}

// literal matches a string, template or regex literal in a test line: a
// quoted string (", ', `) or a /regex/ after ( , = : or a space.
var literal = regexp.MustCompile("\"(?:\\\\.|[^\"\\\\])*\"|'(?:\\\\.|[^'\\\\])*'|`[^`]*`|([(,=:\\s])/(?:\\\\.|[^/\\\\\\n])+/[dgimsuy]*")

// shape is a line with its literals blanked: two assertions with one
// shape check the same thing against other text.
func shape(line string) string {
	return literal.ReplaceAllStringFunc(strings.TrimSpace(line), func(m string) string {
		if strings.HasSuffix(m, "/") || strings.ContainsAny(m[:1], "(,=: \t") {
			return m[:1] + "/…/"
		}
		return "\"…\""
	})
}

// literals lists a line's literals, for the report of a copy update.
func literals(line string) string {
	var out []string
	for _, m := range literal.FindAllString(strings.TrimSpace(line), -1) {
		if strings.ContainsAny(m[:1], "(,=: \t") {
			m = m[1:]
		}
		out = append(out, m)
	}
	return strings.Join(out, ", ")
}

// looseMatchers are matchers that accept almost anything: replacing a
// precise one with one of these weakens the test.
var looseMatchers = regexp.MustCompile(`\.(toBeTruthy|toBeDefined|toBeFalsy|toBeUndefined|not\.toBeNull|toBeNull|anything)\(|\b(assert|require)\.(NotNil|NotEmpty|NotZero|True|False|Nil|Empty|Zero)\(`)

var (
	jsMatcher = regexp.MustCompile(`\.(not\.)?to[A-Z]\w*\(`)
	goMatcher = regexp.MustCompile(`\b(assert|require)\.\w+\((.*)\)\s*$`)
)

// subject is what an assertion checks: in JavaScript the expression
// before the matcher (expect(total)), in testify the last argument
// (got.Name); "" when neither reads.
func subject(line string) string {
	line = strings.TrimSpace(line)
	if m := goMatcher.FindStringSubmatch(line); m != nil {
		args := strings.Split(m[2], ",")
		return strings.TrimSpace(args[len(args)-1])
	}
	if loc := jsMatcher.FindStringIndex(line); loc != nil {
		return line[:loc[0]]
	}
	return ""
}

// parseTestDiff reads `git diff -U0` output and returns the findings in
// the order of the diff.
func parseTestDiff(diff []byte) []testFinding {
	findings, _ := parseTestDiffNotes(diff)
	return findings
}

// parseTestDiffNotes is parseTestDiff with the notes for the report: each
// assertion whose expected text changed, old and new, so a copy update
// passes in plain sight.
func parseTestDiffNotes(diff []byte) ([]testFinding, []string) {
	var findings []testFinding
	var notes []string
	var cur *fileDiff
	var oldLine, newLine int
	var inHunk bool
	var prevAdded string
	flush := func() {
		if cur == nil || !isTestFile(cur.path) {
			return
		}
		if cur.deleted {
			findings = append(findings, testFinding{File: cur.path, Message: "test file deleted"})
			return
		}
		// A skip that was already there and only moved or re-indented
		// is not a new one.
		for i, f := range cur.addedSkips {
			key := cur.addedSkipText[i]
			if cur.removedSkips[key] > 0 {
				cur.removedSkips[key]--
				continue
			}
			findings = append(findings, f)
		}
		// Pair each removed assertion with an added one of the same shape
		// (only the expected text changed: reported, allowed), or with one
		// that swapped its matcher for a loose one (refused).
		used := make([]bool, len(cur.addedAssert))
		var kept []testFinding
		for i, f := range cur.removedAsserts {
			old := cur.removedAssertText[i]
			paired := false
			for j, a := range cur.addedAssert {
				if used[j] {
					continue
				}
				switch {
				case shape(a.text) == shape(old):
					used[j], paired = true, true
					if a.text != strings.TrimSpace(old) {
						notes = append(notes, fmt.Sprintf("%s:%d expected text changed: %s → %s", cur.path, a.line, literals(old), literals(a.text)))
					}
				case looseMatchers.MatchString(a.text) && !looseMatchers.MatchString(old) && subject(a.text) != "" && subject(a.text) == subject(old):
					used[j], paired = true, true
					findings = append(findings, testFinding{File: cur.path, Line: a.line, Message: "matcher loosened: " + strings.TrimSpace(old) + " → " + strings.TrimSpace(a.text)})
				}
				if paired {
					break
				}
			}
			if !paired {
				kept = append(kept, f)
			}
		}
		cur.removedAsserts = kept
		cur.addedAssertions = 0
		for j := range cur.addedAssert {
			if !used[j] {
				cur.addedAssertions++
			}
		}
		if n := len(cur.removedAsserts); n > cur.addedAssertions {
			for _, f := range cur.removedAsserts {
				f.Message = fmt.Sprintf("assertion removed (%d removed, %d added in this file)", n, cur.addedAssertions)
				findings = append(findings, f)
			}
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(diff))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			cur = &fileDiff{removedSkips: map[string]int{}}
			inHunk = false
			// The path until a header names it: "diff --git a/x b/x".
			if i := strings.LastIndex(line, " b/"); i >= 0 {
				cur.path = line[i+3:]
			}
			continue
		}
		if cur == nil {
			continue
		}
		if !inHunk {
			switch {
			case strings.HasPrefix(line, "deleted file mode"):
				cur.deleted = true
			case strings.HasPrefix(line, "--- a/") && cur.deleted:
				cur.path = strings.TrimPrefix(line, "--- a/")
			case strings.HasPrefix(line, "+++ b/"):
				cur.path = strings.TrimPrefix(line, "+++ b/")
			case strings.HasPrefix(line, "rename to "):
				cur.path = strings.TrimPrefix(line, "rename to ")
			}
		}
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			inHunk = true
			oldLine, _ = strconv.Atoi(m[1])
			newLine, _ = strconv.Atoi(m[2])
			prevAdded = ""
			continue
		}
		if !inHunk || line == "" {
			continue
		}
		goFile := strings.HasSuffix(cur.path, "_test.go")
		skip, assert := jsSkip, jsAssert
		if goFile {
			skip, assert = goSkip, goAssert
		}
		text := line[1:]
		switch line[0] {
		case '-':
			if !isComment(text) {
				if skip.MatchString(text) {
					cur.removedSkips[strings.TrimSpace(text)]++
				}
				if assert.MatchString(text) {
					cur.removedAsserts = append(cur.removedAsserts, testFinding{File: cur.path, Line: oldLine, Old: true})
					cur.removedAssertText = append(cur.removedAssertText, strings.TrimSpace(text))
				}
			}
			oldLine++
		case '+':
			if !isComment(text) {
				if skip.MatchString(text) && !allowSkip.MatchString(text) && !(isComment(prevAdded) && allowSkip.MatchString(prevAdded)) {
					what := "skip added"
					if strings.Contains(text, ".only") || strings.Contains(text, "fit(") || strings.Contains(text, "fdescribe(") {
						what = "only added (the other tests stop running)"
					}
					cur.addedSkips = append(cur.addedSkips, testFinding{File: cur.path, Line: newLine, Message: what + ": " + strings.TrimSpace(text)})
					cur.addedSkipText = append(cur.addedSkipText, strings.TrimSpace(text))
				}
				if assert.MatchString(text) {
					cur.addedAssertions++
					cur.addedAssert = append(cur.addedAssert, assertLine{newLine, strings.TrimSpace(text)})
				}
			}
			prevAdded = text
			newLine++
		}
	}
	flush()
	return findings, notes
}

// testGuard is the verify step: the findings fail it unless the change
// is confirmed with the flag or the environment.
func testGuard(ctx context.Context, dir string, allowFlag bool) (string, error) {
	findings, notes, err := weakenedTests(ctx, dir)
	if err != nil || len(findings) == 0 {
		return notes, err
	}
	switch {
	case allowFlag:
		return fmt.Sprintf("%d test change(s) confirmed with --allow-test-changes", len(findings)), nil
	case allowTestChanges():
		return fmt.Sprintf("%d test change(s) confirmed with %s", len(findings), EnvAllowTestChanges), nil
	}
	return "", errors.New(testGuardMessage(findings))
}

// testGuardMessage is the failure of the verify step: each finding, then
// how to confirm a deliberate change.
func testGuardMessage(findings []testFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the staged changes weaken the tests (%d finding(s)):\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	fmt.Fprintf(&b, "A test is never weakened to make it pass: if a test is wrong, say so to the developer.\n")
	fmt.Fprintf(&b, "A deliberate change is confirmed with %s=1 git commit ... (or lidza verify --allow-test-changes); one skip with a reason passes with a `%s <reason>` comment on its line or the line above.", EnvAllowTestChanges, allowSkipMarker)
	return b.String()
}
