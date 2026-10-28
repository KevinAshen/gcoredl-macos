package main

type entityRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type entity struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		Title        string `json:"title"`
		Author       string `json:"author"`
		Cover        string `json:"cover"`
		Audio        string `json:"audio"`
		MediaType    string `json:"media-type"`
		OriginalSrc  string `json:"original-src"`
		VendorSrc    string `json:"vendor-src"`
		PlaybackType string `json:"playback-type"`
		IsFree       bool   `json:"is-free"`
	} `json:"attributes"`
	Relationships struct {
		Media struct {
			Data entityRef `json:"data"`
		} `json:"media"`
		PublishedAudiobooks struct {
			Data []entityRef `json:"data"`
		} `json:"published-audiobooks"`
	} `json:"relationships"`
}

type entityResponse struct {
	Data     entity   `json:"data"`
	Included []entity `json:"included"`
}

type album struct {
	ID       string
	Title    string
	Author   string
	Cover    string
	Chapters []chapter
}

type chapter struct {
	ID        string
	Title     string
	IsFree    bool
	VendorSrc string
	Media     media
}

type media struct {
	ID           string
	Audio        string
	MediaType    string
	OriginalSrc  string
	PlaybackType string
}
