package marketdata

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// ParseFREDCSV parses a FRED CSV response ("DATE,VALUE" or "observation_date,SERIES")
// skipping missing "." observations cleanly.
func ParseFREDCSV(raw []byte, seriesID string, updatedAt int64) ([]MacroPoint, error) {
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read FRED CSV header: %w", err)
	}
	if len(header) < 2 {
		return nil, fmt.Errorf("unexpected FRED CSV header column count %d", len(header))
	}

	out := make([]MacroPoint, 0, 128)
	for {
		rec, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("parse FRED CSV row: %w", readErr)
		}
		if len(rec) < 2 {
			continue
		}
		dateStr := strings.TrimSpace(rec[0])
		valStr := strings.TrimSpace(rec[1])
		if dateStr == "" || valStr == "" || valStr == "." {
			continue
		}
		val, parseErr := strconv.ParseFloat(valStr, 64)
		if parseErr != nil || math.IsNaN(val) || math.IsInf(val, 0) {
			continue
		}
		out = append(out, MacroPoint{
			SeriesID:  seriesID,
			Date:      dateStr,
			Value:     val,
			Source:    VenueFRED,
			UpdatedAt: updatedAt,
		})
	}
	return out, nil
}

// ParseSECSubmissions extracts recent filings from SEC EDGAR CIK submissions JSON.
func ParseSECSubmissions(raw []byte, cik string, limit int) ([]SECFiling, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var payload struct {
		CIK     string `json:"cik"`
		Name    string `json:"name"`
		Filings struct {
			Recent struct {
				AccessionNumber []string `json:"accessionNumber"`
				FilingDate      []string `json:"filingDate"`
				Form            []string `json:"form"`
				PrimaryDocument []string `json:"primaryDocument"`
				PrimaryDocDesc  []string `json:"primaryDocDescription"`
			} `json:"recent"`
		} `json:"filings"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode SEC submissions: %w", err)
	}
	rec := payload.Filings.Recent
	n := len(rec.AccessionNumber)
	if len(rec.FilingDate) < n {
		n = len(rec.FilingDate)
	}
	if len(rec.Form) < n {
		n = len(rec.Form)
	}
	if len(rec.PrimaryDocument) < n {
		n = len(rec.PrimaryDocument)
	}
	if n > limit {
		n = limit
	}
	numericCIK := strings.TrimLeft(cik, "0")
	if numericCIK == "" {
		numericCIK = "0"
	}
	out := make([]SECFiling, 0, n)
	for i := 0; i < n; i++ {
		acc := strings.TrimSpace(rec.AccessionNumber[i])
		accNoDashes := strings.ReplaceAll(acc, "-", "")
		doc := strings.TrimSpace(rec.PrimaryDocument[i])
		var desc string
		if i < len(rec.PrimaryDocDesc) {
			desc = strings.TrimSpace(rec.PrimaryDocDesc[i])
		}
		var docURL string
		if accNoDashes != "" && doc != "" {
			docURL = fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/%s", numericCIK, accNoDashes, doc)
		}
		out = append(out, SECFiling{
			CIK:             cik,
			Company:         strings.TrimSpace(payload.Name),
			Form:            strings.TrimSpace(rec.Form[i]),
			FilingDate:      strings.TrimSpace(rec.FilingDate[i]),
			AccessionNumber: acc,
			PrimaryDocument: doc,
			URL:             docURL,
			Description:     desc,
		})
	}
	return out, nil
}

func (c *upstreamClient) fetchFREDSeries(ctx context.Context, seriesID string, updatedAt int64) ([]MacroPoint, error) {
	id, err := NormalizeSeriesID(seriesID)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("https://fred.stlouisfed.org/graph/fredgraph.csv?id=%s", url.QueryEscape(id))
	raw, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	return ParseFREDCSV(raw, id, updatedAt)
}

func (c *upstreamClient) fetchSECFilings(ctx context.Context, cik string, limit int) ([]SECFiling, error) {
	padded, err := NormalizeCIK(cik)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("https://data.sec.gov/submissions/CIK%s.json", url.PathEscape(padded))
	raw, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	return ParseSECSubmissions(raw, padded, limit)
}
