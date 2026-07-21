package domain

// CalCMSEvent is the subset of an event response used by the application.
type CalCMSEvent struct {
	EventID int    `json:"event_id"`
	Skey    string `json:"skey"`
}

// CalCMSEventResponse is the relevant envelope returned by calCMS.
type CalCMSEventResponse struct {
	Events []CalCMSEvent `json:"events"`
}
