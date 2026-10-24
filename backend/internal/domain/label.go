package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxLabelNameLen = 50

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type Label struct {
	ID        ID
	BoardID   ID
	Name      string
	Color     string // #rrggbb, lowercase
	CreatedAt time.Time
}

// LabelFields validates the label fields that are given (nil = not given)
// and normalizes them: the name trimmed, the color lowercase.
func LabelFields(name, color *string) (*string, *string, error) {
	var n, c *string
	fields := map[string]string{}
	if name != nil {
		v := strings.TrimSpace(*name)
		switch {
		case v == "":
			fields["name"] = "El nombre es obligatorio"
		case utf8.RuneCountInString(v) > MaxLabelNameLen:
			fields["name"] = fmt.Sprintf("El nombre no puede superar %d caracteres", MaxLabelNameLen)
		}
		n = &v
	}
	if color != nil {
		if !hexColor.MatchString(*color) {
			fields["color"] = "El color debe ser hexadecimal, como #3ee6b0"
		}
		v := strings.ToLower(*color)
		c = &v
	}
	if len(fields) > 0 {
		return nil, nil, &ValidationError{Fields: fields}
	}
	return n, c, nil
}
