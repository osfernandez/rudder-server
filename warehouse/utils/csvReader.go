package warehouseutils

import (
	"encoding/csv"
	"io"
)

type csvReader struct {
	reader *csv.Reader
}

func (csv *csvReader) Read(columnNames []string) (record []string, err error) {
	record, err = csv.reader.Read()
	return
}

func NewCsvReader(r io.Reader) *csvReader {
	return &csvReader{reader: csv.NewReader(r)}
}
