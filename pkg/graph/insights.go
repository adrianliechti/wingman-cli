package graph

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// Bounds on the insights history walk: at most this many commits, none older
// than the weekly window. Merge commits count as activity but are excluded
// from file churn, where their first-parent diff would double-attribute every
// merged change; oversized commits (vendor drops, bulk renames) are excluded
// from churn too.
const (
	insightsMaxCommits    = 2000
	insightsWeeks         = 26
	insightsTopAuthors    = 10
	insightsTopChurn      = 15
	insightsMaxChurnFiles = 200
	insightsTopSeries     = 4
	insightsTopModules    = 8
)

type WeekActivity struct {
	Week    string `json:"week"`
	Commits int    `json:"commits"`
}

type AuthorStat struct {
	Name    string    `json:"name"`
	Commits int       `json:"commits"`
	Files   int       `json:"files"`
	Last    time.Time `json:"last"`
}

type ChurnStat struct {
	File    string `json:"file"`
	Commits int    `json:"commits"`
	Authors int    `json:"authors"`
}

type AuthorSeries struct {
	Name  string `json:"name"`
	Weeks []int  `json:"weeks"`
}

type ModuleActivity struct {
	Module  string `json:"module"`
	Commits int    `json:"commits"`
}

type GitInsightsResult struct {
	Commits     int              `json:"commits"`
	Since       time.Time        `json:"since"`
	Weeks       []WeekActivity   `json:"weeks"`
	AuthorWeeks []AuthorSeries   `json:"author_weeks"`
	Punch       [7][24]int       `json:"punch"`
	Authors     []AuthorStat     `json:"authors"`
	Modules     []ModuleActivity `json:"modules"`
	Churn       []ChurnStat      `json:"churn"`
}

func (e *Engine) GitInsights(ctx context.Context) (GitInsightsResult, error) {
	return gitInsights(ctx, e.root)
}

func topPathSegment(file string) string {
	if first, _, ok := strings.Cut(file, "/"); ok {
		return first
	}
	return "(root)"
}

func weekStart(t time.Time) time.Time {
	t = t.Local()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	offset := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -offset)
}

func gitInsights(ctx context.Context, root string) (GitInsightsResult, error) {
	res := GitInsightsResult{}

	repo, err := git.PlainOpen(root)
	if err != nil {
		return res, err
	}
	head, err := repo.Head()
	if err != nil {
		return res, err
	}
	iter, err := repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return res, err
	}
	defer iter.Close()

	now := time.Now()
	currentWeek := weekStart(now)
	cutoff := currentWeek.AddDate(0, 0, -7*(insightsWeeks-1))

	weekCommits := map[string]int{}
	weekAuthorCommits := map[string]map[string]int{}
	moduleCommits := map[string]int{}
	type authorAgg struct {
		commits int
		files   map[string]bool
		last    time.Time
	}
	authors := map[string]*authorAgg{}
	type churnAgg struct {
		commits int
		authors map[string]bool
	}
	churn := map[string]*churnAgg{}

	err = iter.ForEach(func(c *object.Commit) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		when := c.Author.When
		if when.Before(cutoff) || res.Commits >= insightsMaxCommits {
			return storer.ErrStop
		}
		res.Commits++
		if res.Since.IsZero() || when.Before(res.Since) {
			res.Since = when
		}

		week := weekStart(when).Format("2006-01-02")
		weekCommits[week]++

		name := c.Author.Name
		if name == "" {
			name = c.Author.Email
		}
		if weekAuthorCommits[week] == nil {
			weekAuthorCommits[week] = map[string]int{}
		}
		weekAuthorCommits[week][name]++

		local := when.Local()
		res.Punch[(int(local.Weekday())+6)%7][local.Hour()]++

		author := authors[name]
		if author == nil {
			author = &authorAgg{files: map[string]bool{}}
			authors[name] = author
		}
		author.commits++
		if when.After(author.last) {
			author.last = when
		}

		if c.NumParents() > 1 {
			return nil
		}
		files, err := commitFiles(c)
		if err != nil || len(files) > insightsMaxChurnFiles {
			return nil
		}
		modules := map[string]bool{}
		for f := range files {
			author.files[f] = true
			entry := churn[f]
			if entry == nil {
				entry = &churnAgg{authors: map[string]bool{}}
				churn[f] = entry
			}
			entry.commits++
			entry.authors[name] = true
			modules[topPathSegment(f)] = true
		}
		for m := range modules {
			moduleCommits[m]++
		}
		return nil
	})
	if err != nil && err != storer.ErrStop {
		return res, err
	}

	for i := insightsWeeks - 1; i >= 0; i-- {
		week := currentWeek.AddDate(0, 0, -7*i).Format("2006-01-02")
		res.Weeks = append(res.Weeks, WeekActivity{Week: week, Commits: weekCommits[week]})
	}

	for name, agg := range authors {
		res.Authors = append(res.Authors, AuthorStat{
			Name:    name,
			Commits: agg.commits,
			Files:   len(agg.files),
			Last:    agg.last,
		})
	}
	sort.Slice(res.Authors, func(i, j int) bool {
		if res.Authors[i].Commits != res.Authors[j].Commits {
			return res.Authors[i].Commits > res.Authors[j].Commits
		}
		return res.Authors[i].Name < res.Authors[j].Name
	})

	series := res.Authors
	if len(series) > insightsTopSeries {
		series = series[:insightsTopSeries]
	}
	top := map[string]int{}
	for i, a := range series {
		res.AuthorWeeks = append(res.AuthorWeeks, AuthorSeries{
			Name:  a.Name,
			Weeks: make([]int, len(res.Weeks)),
		})
		top[a.Name] = i
	}
	others := AuthorSeries{Name: "others", Weeks: make([]int, len(res.Weeks))}
	hasOthers := false
	for i, w := range res.Weeks {
		for name, n := range weekAuthorCommits[w.Week] {
			if idx, ok := top[name]; ok {
				res.AuthorWeeks[idx].Weeks[i] = n
			} else {
				others.Weeks[i] += n
				hasOthers = true
			}
		}
	}
	if hasOthers {
		res.AuthorWeeks = append(res.AuthorWeeks, others)
	}

	if len(res.Authors) > insightsTopAuthors {
		res.Authors = res.Authors[:insightsTopAuthors]
	}

	for m, n := range moduleCommits {
		res.Modules = append(res.Modules, ModuleActivity{Module: m, Commits: n})
	}
	sort.Slice(res.Modules, func(i, j int) bool {
		if res.Modules[i].Commits != res.Modules[j].Commits {
			return res.Modules[i].Commits > res.Modules[j].Commits
		}
		return res.Modules[i].Module < res.Modules[j].Module
	})
	if len(res.Modules) > insightsTopModules {
		res.Modules = res.Modules[:insightsTopModules]
	}

	for f, agg := range churn {
		res.Churn = append(res.Churn, ChurnStat{File: f, Commits: agg.commits, Authors: len(agg.authors)})
	}
	sort.Slice(res.Churn, func(i, j int) bool {
		if res.Churn[i].Commits != res.Churn[j].Commits {
			return res.Churn[i].Commits > res.Churn[j].Commits
		}
		return res.Churn[i].File < res.Churn[j].File
	})
	if len(res.Churn) > insightsTopChurn {
		res.Churn = res.Churn[:insightsTopChurn]
	}

	return res, nil
}
