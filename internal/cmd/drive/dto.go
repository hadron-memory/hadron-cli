package drive

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type fileDTO struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	MimeType   string   `json:"mimeType"`
	Parents    []string `json:"parents"`
	Size       *float64 `json:"size"`
	CreatedAt  *string  `json:"createdAt"`
	ModifiedAt *string  `json:"modifiedAt"`
	WebLink    *string  `json:"webLink"`
}
type pageDTO struct {
	Files         []fileDTO `json:"files"`
	NextPageToken *string   `json:"nextPageToken"`
}
type exportDTO struct {
	File     fileDTO `json:"file"`
	Format   string  `json:"format"`
	MimeType string  `json:"mimeType"`
	Content  string  `json:"content"`
}

func protocol() error {
	return exitcode.Newf(exitcode.Unavailable, "server returned an incomplete Drive response")
}
func project(v gen.DriveReadFile) (fileDTO, error) {
	if v.Parents == nil {
		return fileDTO{}, protocol()
	}
	parents := append([]string{}, v.Parents...)
	return fileDTO{v.Id, v.Name, string(v.Kind), v.MimeType, parents, v.Size, v.CreatedAt, v.ModifiedAt, v.WebLink}, nil
}
