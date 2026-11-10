package pages

// A single canonical, resolved instruction is shown at a time. These fields
// are presentation only; selection never changes the signed publication review.
type TrainingPublicationInstructionChoice struct {
	Key, Label string
}

type TrainingPublicationInstructionReview struct {
	Choices     []TrainingPublicationInstructionChoice
	SelectedKey string
	Athlete     string
	Session     StructuredTrainingSession
}
