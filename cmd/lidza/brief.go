package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/agim/lidza/pkg/brief"
)

// runBrief is the kickoff interview and its parts:
//
//	lidza brief                  the open questions, one by one
//	lidza brief --all            every question, the current answer kept on Enter
//	lidza brief --list           the questions, their ids and answers
//	lidza brief answer <id> ".." one answer, without prompts
//	lidza brief skip [id...]     skip questions for good; no ids: every open one
func runBrief(_ context.Context, args []string) error {
	if len(args) > 0 && args[0] == "skip" {
		fs := flags("brief skip")
		dir := fs.String("dir", ".", "project directory")
		rest := args[1:]
		var ids []string
		for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			ids, rest = append(ids, rest[0]), rest[1:]
		}
		if err := fs.Parse(rest); err != nil {
			return err
		}
		ids = append(ids, fs.Args()...)
		abs, cfg, err := loadProject(*dir)
		if err != nil || cfg == nil {
			return errors.New("brief needs a lidza.json project")
		}
		done, err := brief.Skip(abs, cfg.Name, ids...)
		if err != nil {
			return err
		}
		if len(done) == 0 {
			fmt.Println("nothing to skip: those questions are answered or skipped already")
			return nil
		}
		fmt.Printf("skipped %d question(s): %s; agents will not ask them, and an answer given later (lidza brief --all) replaces the skip\n", len(done), strings.Join(done, ", "))
		return nil
	}
	if len(args) > 0 && args[0] == "answer" {
		fs := flags("brief answer")
		dir := fs.String("dir", ".", "project directory")
		rest := args[1:]
		var pos []string
		for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			pos, rest = append(pos, rest[0]), rest[1:]
		}
		if err := fs.Parse(rest); err != nil {
			return err
		}
		pos = append(pos, fs.Args()...)
		if len(pos) < 1 {
			return errors.New("brief answer <id> \"answer\" (an empty answer reopens the question)")
		}
		abs, cfg, err := loadProject(*dir)
		if err != nil || cfg == nil {
			return errors.New("brief needs a lidza.json project")
		}
		res, err := brief.Answer(abs, cfg.Name, pos[0], strings.Join(pos[1:], " "))
		if err != nil {
			return err
		}
		printBriefResult(os.Stdout, res)
		return nil
	}
	fs := flags("brief")
	dir := fs.String("dir", ".", "project directory")
	all := fs.Bool("all", false, "ask every question, keeping the current answer on Enter")
	list := fs.Bool("list", false, "print the questions, their ids and answers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, cfg, err := loadProject(*dir)
	if err != nil || cfg == nil {
		return errors.New("brief needs a lidza.json project")
	}
	if _, err := brief.Ensure(abs, cfg.Name); err != nil {
		return err
	}
	if *list {
		b, _ := brief.Load(abs)
		section := ""
		for _, q := range brief.Questions {
			if q.Section != section {
				section = q.Section
				fmt.Printf("\n%s\n", section)
			}
			answer := b.Answers[q.ID]
			switch {
			case answer != "":
			case b.Skipped[q.ID]:
				answer = "(skipped)"
			default:
				answer = "(open)"
			}
			fmt.Printf("  %-14s %s\n  %-14s %s\n", q.ID, q.Ask, "", strings.ReplaceAll(answer, "\n", " "))
		}
		return nil
	}
	if !isTerminal(os.Stdin) {
		return errors.New("brief asks its questions in a terminal; an agent uses the MCP tools lidza_brief and lidza_brief_answer (recipe \"Start with the brief\"), a script lidza brief answer <id> \"...\"")
	}
	return interview(abs, cfg.Name, *all, bufio.NewReader(os.Stdin), os.Stdout)
}

// interview asks the open questions (every one with all) and records the
// answers as they come; q stops, Enter skips.
func interview(dir, app string, all bool, in *bufio.Reader, out io.Writer) error {
	b, err := brief.Load(dir)
	if err != nil {
		return err
	}
	questions := b.Open()
	if all {
		questions = brief.Questions
	}
	if len(questions) == 0 {
		fmt.Fprintln(out, "The brief has every answer; lidza brief --all goes through them again.")
		return nil
	}
	fmt.Fprintf(out, "The brief for %s: %d question(s). Each answer is saved in %s and applied at once.\n", app, len(questions), brief.File)
	fmt.Fprintln(out, "Pick a number (several, like 1,3, where more than one fits) or type your own answer.")
	fmt.Fprintln(out, "Enter asks again later; s skips the question for good; S skips all the rest; q stops for now.")
	answered := 0
	for i, q := range questions {
		current := b.Answers[q.ID]
		fmt.Fprintf(out, "\n[%s %d/%d] %s\n  %s\n", q.Section, i+1, len(questions), q.Ask, q.Why)
		for n, s := range q.Suggestions {
			line := fmt.Sprintf("  %d) %s", n+1, s.Value)
			if s.Detail != "" {
				line += ": " + s.Detail
			}
			fmt.Fprintln(out, line)
		}
		if current != "" {
			fmt.Fprintf(out, "  now: %s (Enter keeps it)\n", strings.ReplaceAll(current, "\n", " "))
		}
		fmt.Fprint(out, "> ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			break
		}
		line = strings.TrimSpace(line)
		if line == "q" || line == "quit" {
			break
		}
		if line == "" {
			continue
		}
		if line == "s" || line == "skip" {
			if _, err := brief.Skip(dir, app, q.ID); err != nil {
				return err
			}
			fmt.Fprintln(out, "  skipped")
			continue
		}
		if line == "S" || line == "skip all" {
			var rest []string
			for _, r := range questions[i:] {
				if b.Answers[r.ID] == "" {
					rest = append(rest, r.ID)
				}
			}
			done, err := brief.Skip(dir, app, rest...)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "  skipped the remaining %d question(s)\n", len(done))
			break
		}
		line = brief.Clean(q, line)
		if line == "" {
			fmt.Fprintln(out, "  that was the question itself; nothing saved (lidza brief asks it again)")
			continue
		}
		answer := resolveChoice(q, line)
		res, err := brief.Answer(dir, app, q.ID, answer)
		if err != nil {
			return err
		}
		answered++
		fmt.Fprintf(out, "  saved: %s\n", answer)
		printBriefResult(out, res)
	}
	b, _ = brief.Load(dir)
	fmt.Fprintf(out, "\n%d answer(s) saved; %d question(s) still open (lidza brief continues), %d skipped.\n", answered, len(b.Open()), len(b.Skipped))
	fmt.Fprintf(out, "Commit them for the team: git add %s docs CLAUDE.md AGENTS.md GEMINI.md src admin && git commit -m \"Brief\"\n", brief.File)
	return nil
}

// resolveChoice turns "2" or "1,3" into the suggestions' values; anything
// else is the developer's own answer. A number after the choices ("2:
// with EU hosting") keeps the added words.
func resolveChoice(q brief.Question, line string) string {
	head, extra, _ := strings.Cut(line, ":")
	var picked []string
	for _, part := range strings.Split(head, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > len(q.Suggestions) {
			return line
		}
		picked = append(picked, q.Suggestions[n-1].Value)
	}
	// Only a "one" question takes a single pick; a "many" question and a
	// free answer with suggestions combine them.
	if q.Kind == brief.One && len(picked) > 1 {
		return line
	}
	answer := strings.Join(picked, ", ")
	if extra = strings.TrimSpace(extra); extra != "" {
		answer += ": " + extra
	}
	return answer
}

func printBriefResult(out io.Writer, res brief.Result) {
	if res.Decision != "" {
		fmt.Fprintf(out, "  decision recorded: %s\n", res.Decision)
	}
	if res.Recipe != "" {
		fmt.Fprintf(out, "  recipe seeded: %s (docs/lidza-guide.md, App recipes)\n", res.Recipe)
	}
	if len(res.Files) > 1 {
		fmt.Fprintf(out, "  files: %s\n", strings.Join(res.Files, ", "))
	}
	for _, m := range res.Manual {
		fmt.Fprintf(out, "  for the agent: %s\n", m)
	}
}

// isTerminal reports whether f is an interactive terminal (not /dev/null,
// which is a character device too, nor a pipe).
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// runNote is `lidza note add "..."`: a lasting fact for the team, in the
// agent files' Team notes.
func runNote(_ context.Context, args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return errors.New("note add \"text\": a lasting fact about this app for the team and every agent, in CLAUDE.md, AGENTS.md and GEMINI.md")
	}
	fs := flags("note add")
	dir := fs.String("dir", ".", "project directory")
	rest := args[1:]
	var text []string
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		text, rest = append(text, rest[0]), rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	text = append(text, fs.Args()...)
	abs, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	files, err := brief.AddNote(abs, strings.Join(text, " "))
	if err != nil {
		return err
	}
	fmt.Printf("noted in %s\n", strings.Join(files, ", "))
	return nil
}
