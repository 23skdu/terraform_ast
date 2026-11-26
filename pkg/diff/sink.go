package diff

// diffSink is the common destination for difference items. Buffered Compare appends to a
// DiffReport; CompareStreamContext forwards each item immediately and keeps only counters.
// A cancelled stream marks the sink aborted, after which all further items are ignored without
// adding branches to the comparison phases themselves.
type diffSink struct {
	report  *DiffReport
	emit    func(DiffStreamEventKind, DifferenceItem) bool
	aborted bool
}

func (s *diffSink) breaking(item DifferenceItem) {
	s.add(DiffStreamBreaking, item)
}

func (s *diffSink) warning(item DifferenceItem) {
	s.add(DiffStreamWarning, item)
}

func (s *diffSink) feature(item DifferenceItem) {
	s.add(DiffStreamFeature, item)
}

func (s *diffSink) add(kind DiffStreamEventKind, item DifferenceItem) {
	if s.aborted {
		return
	}
	scoreDifference(kind, &item)
	if s.emit != nil {
		if !s.emit(kind, item) {
			s.aborted = true
		}
		return
	}
	switch kind {
	case DiffStreamBreaking:
		s.report.BreakingChanges = append(s.report.BreakingChanges, item)
	case DiffStreamWarning:
		s.report.Warnings = append(s.report.Warnings, item)
	case DiffStreamFeature:
		s.report.NewFeatures = append(s.report.NewFeatures, item)
	}
}

// scoreDifference assigns the same deterministic confidence score used by buffered Compare.
// Scoring at emission time lets streaming reports preserve confidence without buffering items.
func scoreDifference(kind DiffStreamEventKind, item *DifferenceItem) {
	ev := item.Evidence
	switch kind {
	case DiffStreamWarning:
		var base int
		switch item.Category {
		case "TerraformCore", "ProviderRequirement":
			base = 7
		case "VariableValidation":
			base = 6
		case "VariableType", "OutputType":
			base = 8
		default:
			base = 9
		}
		item.ConfidenceScore = base + ev
	default:
		if ev < 1 {
			ev = 1
		}
		item.ConfidenceScore = 8 + (ev - 1)
	}
}
