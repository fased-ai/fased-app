//go:build !linux

package core

import (
	"context"
	"errors"
	"io"
)

func exportEnrollmentImage(context.Context, string) (EnrollmentImage, io.Closer, error) {
	return EnrollmentImage{}, nil, errors.New("same-instance enrollment requires Linux")
}
func validateEnrollmentImage(EnrollmentImage) error {
	return errors.New("same-instance enrollment requires Linux")
}
func importEnrollmentImage(context.Context, string, EnrollmentImage) error {
	return errors.New("same-instance enrollment requires Linux")
}
