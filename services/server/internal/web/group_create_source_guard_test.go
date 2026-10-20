package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Only the two retained Q6 mutation inventories use their original 27 rows.
// All current custody readers still traverse the complete 31-row chain.
func groupCreatePreviousRepairRows() []guiPublicGetRepairRow {
	rows := groupDetailPreviousRepairRows(guiPublicGetRepairRows())
	if len(rows) != 31 || rows[0].path != "services/server/internal/web/renderer.go" || rows[0].afterSHA != "6228270fd3343f7d352b79780bd9055ccbf9fe70b2d7917acdb4e0b6ffd31ed3" || rows[1].afterSHA != rows[0].beforeSHA || rows[2].beforeSHA != "94f11a3f1849c919ba100c4ba68236263f71f07741006c1992dadc4020723e68" || rows[3].path != "services/server/internal/web/forms.go" || rows[3].afterSHA != "a9cc66599f770b3e6b30ea3bc4be7148253702c939d484ae6b7bea5d16af6717" || rows[4].afterSHA != rows[3].beforeSHA || rows[5].afterSHA != rows[4].beforeSHA || rows[5].beforeSHA != "6a41190d4f81107c32b7f0f651f59dfc6d1adc298b66eea3861105579360d3cb" || rows[6].path != "services/server/internal/web/server.go" {
		return nil
	}
	previous := append([]guiPublicGetRepairRow(nil), rows[1:3]...)
	return append(previous, rows[6:]...)
}

// The immutable preference registry predates only the group dispatch/metadata
// and its four custody rows. Recover its exact bytes in memory; no registry,
// production file, original source pin or live binding is replaced.
func groupCreatePreferenceSourceBinding(path string, raw []byte, physical, wanted guiG1Binding) (guiG1Binding, error) {
	if len(raw) != physical.Bytes || guiPublicHash(raw) != physical.SHA256 || !guiPublicGetLiveMode(physical.FullMode, wanted.FullMode) {
		return guiG1Binding{}, fmt.Errorf("group-preference-physical-source")
	}
	previous := raw
	var err error
	// Live admission requires the exact newest account bytes before prior controls.
	if path == "services/server/internal/web/renderer.go" || path == "services/server/internal/web/gui_public_get_walk_test.go" {
		raw, err = accountCenterSourcePreimage(path, raw)
		if err != nil {
			return guiG1Binding{}, err
		}
		previous = raw
	}
	// Live tuples require the complete newest inbox source before old inverses.
	if path == "services/server/internal/web/renderer.go" || path == "services/server/internal/web/gui_public_get_walk_test.go" {
		raw, err = notificationInboxSourcePreimage(path, raw)
		if err != nil {
			return guiG1Binding{}, err
		}
		previous = raw
	}
	if path == "services/server/internal/web/gui_preferences_layout_test.go" || path == "services/server/internal/web/gui_preferences_get_transport_test.go" {
		previous, err = notificationPreferencesSourcePreimage(path, raw)
		if err != nil {
			return guiG1Binding{}, err
		}
		raw = previous
	}
	switch path {
	case "services/server/internal/web/renderer.go":
		raw, err = notificationPreferencesRendererPreimage(raw)
		if err != nil {
			return guiG1Binding{}, err
		}
		rows := groupDetailPreviousRepairRows(guiPublicGetRepairRows())
		raw, err = groupDetailRendererPreimage(raw)
		if err != nil {
			return guiG1Binding{}, err
		}
		if groupCreatePreviousRepairRows() == nil {
			return guiG1Binding{}, fmt.Errorf("group-preference-repair-chain")
		}
		previous, err = groupCreateCustodyRowsPreimage(path, raw, rows[:1])
	case "services/server/internal/web/forms.go":
		previous, err = guiPublicGetRowsPreimage(path, raw)
	case "services/server/internal/web/gui_public_get_walk_test.go":
		raw, err = notificationPreferencesWalkerPreimage(raw)
		if err != nil {
			return guiG1Binding{}, err
		}
		raw, err = groupDetailWalkerPreimage(raw)
		if err != nil {
			return guiG1Binding{}, err
		}
		if len(raw) != 61574 || guiPublicHash(raw) != "08a920a05fd36516a0787aa0842e2f19d60996897171a1831e06827a712b3dd2" {
			return guiG1Binding{}, fmt.Errorf("group-preference-complete-source")
		}
		previous = append([]byte(nil), raw...)
		current := []byte("for _, row := range groupCreatePreviousRepairRows() {")
		if bytes.Count(previous, current) != 2 {
			return guiG1Binding{}, fmt.Errorf("group-preference-control-inverse")
		}
		previous = bytes.ReplaceAll(previous, current, []byte("for _, row := range guiPublicGetRepairRows() {"))
		for _, block := range []struct{ start, end string }{
			{"\t\t// ADR-0062: exact selected group dispatch, before the untouched family chain.\n", "\t\t// ADR-0061: exact native family dispatch inverse; all previous rows\n"},
			{"\t\t// Group-only semantic metadata inverses recover the entire original form builder.\n", "\t\t{\n\t\t\tpath:        \"services/server/internal/web/server.go\",\n"},
		} {
			start, end := []byte(block.start), []byte(block.end)
			if bytes.Count(previous, start) != 1 || bytes.Count(previous, end) != 1 {
				return guiG1Binding{}, fmt.Errorf("group-preference-row-inverse")
			}
			first, last := bytes.Index(previous, start), bytes.Index(previous, end)
			if first < 0 || last <= first {
				return guiG1Binding{}, fmt.Errorf("group-preference-row-order")
			}
			previous = append(previous[:first], previous[last:]...)
		}
		if len(previous) != 59333 || guiPublicHash(previous) != "49c2d59891606be901dae56569c916c15f9b7eddb0d4931bfc59a6f41ac0ccea" {
			return guiG1Binding{}, fmt.Errorf("group-preference-exact-source-preimage")
		}
	}
	proof := guiG1Binding{len(previous), guiPublicHash(previous), physical.FullMode}
	if err != nil || !guiPublicGetBindingLive(proof, wanted) {
		return guiG1Binding{}, fmt.Errorf("group-preference-source-preimage")
	}
	return proof, nil
}

func TestNativeGroupPreferenceSourceBridgeKeepsEveryPhysicalBinding(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	var registry guiPreferenceGETRegistry
	if err != nil || guiG1StrictJSON(guiPreferencesGETRegistryBytes, &registry) != nil || guiPreferenceGETRegistryCheck(registry) != nil {
		t.Fatal("unchanged preference registry unavailable")
	}
	actual, err := guiPreferenceGETInputs(root, registry)
	if err != nil || len(actual) != 138 {
		t.Fatal("exact group/preference source bridge refused", err)
	}
	for path, binding := range actual {
		_, physical, err := guiCurrentTransportReadBinding(root, path, 2<<20)
		if err != nil || binding != physical {
			t.Fatal("historical preference tuple leaked into a live source reader", path, err)
		}
	}
	for _, path := range []string{"services/server/internal/web/renderer.go", "services/server/internal/web/forms.go", "services/server/internal/web/gui_public_get_walk_test.go"} {
		raw, physical, err := guiCurrentTransportReadBinding(root, path, 2<<20)
		wanted := registry.Sources[path]
		if err != nil {
			t.Fatal(err)
		}
		if _, err := groupCreatePreferenceSourceBinding(path, raw, physical, wanted); err != nil {
			t.Fatal("exact current source refused", path, err)
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			for _, changed := range [][]byte{append(append([]byte(nil), raw...), '\n'), raw[:len(raw)-1]} {
				binding := guiG1Binding{len(changed), guiPublicHash(changed), physical.FullMode}
				if _, err := groupCreatePreferenceSourceBinding(path, changed, binding, wanted); err == nil {
					t.Fatal("unsupported rehashed physical source admitted")
				}
			}
			badMode := physical
			badMode.FullMode |= 0o100
			badPin := wanted
			badPin.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
			if _, err := groupCreatePreferenceSourceBinding(path, raw, badMode, wanted); err == nil {
				t.Fatal("changed executable mode admitted")
			}
			if _, err := groupCreatePreferenceSourceBinding(path, raw, physical, badPin); err == nil {
				t.Fatal("changed original source pin admitted")
			}
			if _, err := groupCreatePreferenceSourceBinding(path, raw, wanted, wanted); err == nil {
				t.Fatal("old registry tuple admitted as current physical bytes")
			}
		})
	}
}

func TestNativeGroupRepairSourceControlsCoverTheNewFormInput(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	source, _, sourceErr := guiPublicGetSources(guiPublicGetSourceBytes)
	inputs, inputErr := guiPublicGetRepairInputs(root, source)
	if err != nil || sourceErr != nil || inputErr != nil || guiPublicGetRepairHistoricalProof(source, inputs) != nil || len(groupCreatePreviousRepairRows()) != 27 || len(groupDetailPreviousRepairRows(guiPublicGetRepairRows())) != 31 {
		t.Fatal("complete old/current repair inventories unavailable")
	}
	const path = "services/server/internal/web/forms.go"
	original := inputs[path]
	former, err := guiPublicGetRowsPreimage(path, original.raw)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"unknown-form": append(append([]byte(nil), original.raw...), '\n'), "mixed-form": former} {
		t.Run(name, func(t *testing.T) {
			changed := map[string]guiPublicGetRepairInput{}
			for name, input := range inputs {
				changed[name] = input
			}
			changed[path] = guiPublicGetRepairInput{raw, guiG1Binding{len(raw), guiPublicHash(raw), original.binding.FullMode}}
			if guiPublicGetRepairHistoricalProof(source, changed) == nil || guiPublicGetRepairHistoricalProof(source, inputs) != nil {
				t.Fatal("new form input drift admitted or current positive control changed")
			}
		})
	}
	// Source is read only: this test never writes the recovered old form bytes.
	physical, err := os.ReadFile(filepath.Join(root, path))
	if err != nil || !bytes.Equal(physical, original.raw) {
		t.Fatal("physical form source changed during source-only controls", err)
	}
}
