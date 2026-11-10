package pages

// TrainingPublicationInstructionChoice identifies the single resolved instruction shown at a time. Its fields
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
