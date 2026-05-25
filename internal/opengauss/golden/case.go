/*-------------------------------------------------------------------------
 *
 * case.go
 *	  Golden-case schema for OpenGauss/GaussDB deterministic evaluation.
 *
 *-------------------------------------------------------------------------
 */
package golden

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Corpus struct {
	Cases []Case `yaml:"cases"`
}

type Case struct {
	ID          string   `yaml:"id"`
	Source      string   `yaml:"source"`
	Tier        int      `yaml:"tier"`
	Database    string   `yaml:"database"`
	ModelTier   string   `yaml:"model_tier"`
	Input       string   `yaml:"input"`
	Command     string   `yaml:"command"`
	Mode        string   `yaml:"mode"`
	RequiresDB  bool     `yaml:"requires_db"`
	RequiresLLM bool     `yaml:"requires_llm"`
	Tags        []string `yaml:"tags"`
	Expected    Expected `yaml:"expected"`
	Quality     Quality  `yaml:"quality"`
}

type Expected struct {
	Intent           string   `yaml:"intent"`
	RouteMode        string   `yaml:"route_mode"`
	Skill            string   `yaml:"skill"`
	Args             string   `yaml:"args"`
	RequiredTools    []string `yaml:"required_tools"`
	ForbiddenTools   []string `yaml:"forbidden_tools"`
	RequiredStrings  []string `yaml:"required_strings"`
	ForbiddenStrings []string `yaml:"forbidden_strings"`
}

type Quality struct {
	MaxLatencyMS                       int           `yaml:"max_latency_ms"`
	MinScore                           int           `yaml:"min_score"`
	MustDistinguishCurrentVsHistorical bool          `yaml:"must_distinguish_current_vs_historical"`
	Rubric                             []RubricCheck `yaml:"rubric"`
}

func LoadFile(path string) (Corpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Corpus{}, err
	}
	var corpus Corpus
	if err := yaml.Unmarshal(data, &corpus); err != nil {
		return Corpus{}, err
	}
	for i, c := range corpus.Cases {
		if c.ID == "" {
			return Corpus{}, fmt.Errorf("case %d missing id", i)
		}
		if c.Input == "" && len(c.Expected.RequiredStrings) == 0 && len(c.Expected.ForbiddenStrings) == 0 {
			return Corpus{}, fmt.Errorf("case %s has no input or output contract", c.ID)
		}
	}
	return corpus, nil
}

type RubricCheck struct {
	Name         string   `yaml:"name"`
	RequiredAll  []string `yaml:"required_all"`
	RequiredAny  []string `yaml:"required_any"`
	ForbiddenAny []string `yaml:"forbidden_any"`
	Points       int      `yaml:"points"`
}
