package models

// DatasetIDs returns all dataset IDs for a template.
func (t *DBTemplate) DatasetIDs() []string {
	ids := make([]string, len(t.Datasets))
	for i, d := range t.Datasets {
		ids[i] = d.ID
	}
	return ids
}

// AsTask converts a PlaygroundChallenge to a TaskDefinition for validation.
func (c *PlaygroundChallenge) AsTask(seminarID string) *TaskDefinition {
	mode := ValidationResultMatch
	if c.ValidationMode != nil {
		mode = *c.ValidationMode
	}

	forbidden := []string{"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "ATTACH", "DETACH", "PRAGMA"}
	if c.ValidationMode != nil && *c.ValidationMode != ValidationResultMatch {
		forbidden = []string{"ATTACH", "DETACH", "PRAGMA"}
	}

	return &TaskDefinition{
		ID:             c.ID,
		SeminarID:      seminarID,
		Title:          c.Title,
		Description:    c.Description,
		Difficulty:     c.Difficulty,
		TaskType:       c.Topic,
		Constructs:     c.Constructs,
		ValidationMode: mode,
		TemplateID:     c.TemplateID,
		DatasetIDs:     c.DatasetIDs,
		StarterSql:     c.StarterSql,
		ExpectedQuery:  c.ExpectedQuery,
		ValidationConfig: ValidationConfig{
			OrderMatters:      true,
			ColumnNamesMatter: true,
			NumericTolerance:  0.001,
			MaxExecutionMs:    5000,
			MaxResultRows:     60,
			ForbiddenKeywords: forbidden,
		},
		ValidationSpec: c.ValidationSpec,
		Hints:          []string{},
	}
}
