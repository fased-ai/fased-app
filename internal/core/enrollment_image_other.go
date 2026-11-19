//go:build !linux

package core

import (
	"context"
	"errors"
	"io"
)

func exportEnrollmentImage(context.Context, string, bool) (EnrollmentImage, io.Closer, error) {
	return EnrollmentImage{}, nil, errors.New("same-instance enrollment requires Linux")
}
func validateEnrollmentImage(EnrollmentImage) error {
	return errors.New("same-instance enrollment requires Linux")
}
func checkEnrollmentStage(context.Context, string, EnrollmentImage, bool) error {
	return errors.New("same-instance enrollment requires Linux")
}
