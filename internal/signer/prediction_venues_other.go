//go:build !linux

package signer

func (*storage) readPredictionKeyFile(string) ([]byte, error) {
	return nil, ErrUnavailable
}

func (*storage) writePredictionKeyFile(string, []byte) error {
	return ErrUnavailable
}
