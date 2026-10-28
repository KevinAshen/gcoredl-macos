package main

import (
	"fmt"
	"strconv"

	"github.com/bogem/id3v2/v2"
)

type tagData struct {
	Title      string
	Album      string
	Artist     string
	Track      int
	TrackTotal int
	Cover      []byte
	CoverMIME  string
}

func writeID3(filename string, data tagData) (err error) {
	tag, err := id3v2.Open(filename, id3v2.Options{Parse: false})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := tag.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	tag.DeleteAllFrames()
	tag.SetDefaultEncoding(id3v2.EncodingUTF8)
	tag.SetGenre("Audiobook")
	tag.SetArtist(data.Artist)
	tag.SetAlbum(data.Album)
	tag.SetTitle(data.Title)
	track := strconv.Itoa(data.Track)
	if data.TrackTotal > 0 {
		track = fmt.Sprintf("%d/%d", data.Track, data.TrackTotal)
	}
	tag.AddTextFrame("TRCK", tag.DefaultEncoding(), track)
	if len(data.Cover) > 0 {
		tag.AddAttachedPicture(id3v2.PictureFrame{
			Encoding:    tag.DefaultEncoding(),
			MimeType:    data.CoverMIME,
			PictureType: 0x03,
			Description: "Cover",
			Picture:     data.Cover,
		})
	}
	return tag.Save()
}
