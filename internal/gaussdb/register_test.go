package gaussdb_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	gdbreg "github.com/sqlrush/opendb/internal/gaussdb"
	ogreg "github.com/sqlrush/opendb/internal/opengauss"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestGaussDBAndOpenGaussSkillParity(t *testing.T) {
	cfg := &config.Config{ConnectionsDir: t.TempDir()}
	ogReg := skill.NewRegistry()
	gdbReg := skill.NewRegistry()
	ogMgr, err := connection.NewManager(cfg)
	if err != nil {
		t.Fatalf("new og manager: %v", err)
	}
	gdbMgr, err := connection.NewManager(cfg)
	if err != nil {
		t.Fatalf("new gaussdb manager: %v", err)
	}

	ogreg.RegisterSkills(ogReg, nil, ogMgr, nil, cfg, nil, t.TempDir())
	ogreg.RegisterAISkills(ogReg, nil, nil, cfg, nil)
	gdbreg.RegisterSkills(gdbReg, nil, gdbMgr, nil, cfg, nil, t.TempDir())
	gdbreg.RegisterAISkills(gdbReg, nil, nil, cfg, nil)

	ogNames := skillNamesForDB(ogReg, "opengauss")
	gdbNames := skillNamesForDB(gdbReg, "gaussdb")
	if !reflect.DeepEqual(ogNames, gdbNames) {
		t.Fatalf("GaussDB skills must match openGauss skills\nog:  %v\ngdb: %v", ogNames, gdbNames)
	}
}

func skillNamesForDB(reg *skill.Registry, dbType string) []string {
	reg.SetActiveDB(dbType)
	var names []string
	for _, s := range reg.All() {
		names = append(names, s.Name())
	}
	sort.Strings(names)
	return names
}
