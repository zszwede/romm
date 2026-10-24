package scan

import (
	"encoding/xml"
	"os"
	"path"
	"strings"
)

// GamelistEntry is one <game> in an ES-DE gamelist.xml.
type GamelistEntry struct {
	Path string `xml:"path"`
	Name string `xml:"name"`
	Desc string `xml:"desc"`
}

type gameList struct {
	Games []GamelistEntry `xml:"game"`
}

// Base returns the entry's file name (gamelist paths are like "./Game (USA).ext").
func (e GamelistEntry) Base() string {
	p := strings.TrimPrefix(strings.ReplaceAll(e.Path, "\\", "/"), "./")
	return path.Base(p)
}

// ParseGamelist reads an ES-DE gamelist.xml and returns its entries. A missing
// file yields (nil, nil) so callers can treat gamelists as optional.
func ParseGamelist(file string) ([]GamelistEntry, error) {
	f, err := os.Open(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var gl gameList
	if err := xml.NewDecoder(f).Decode(&gl); err != nil {
		return nil, err
	}
	return gl.Games, nil
}
