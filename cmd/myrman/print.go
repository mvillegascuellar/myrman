package main

import (
	"database/sql"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/michaelvillegas/myrman/internal/catalog"
)

const catalogTimeLayout = "2006-01-02 15:04:05"

func printPhysicalTable(w io.Writer, rows []catalog.PhysicalBackup) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tSTART TIME\tDURATION\tSTATUS")
	for _, b := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			b.ID,
			b.BackupType,
			formatUnixUTC(b.StartTime),
			formatDuration(b.StartTime, b.EndTime),
			b.Status,
		)
	}
	return tw.Flush()
}

func printBinlogTable(w io.Writer, rows []catalog.BinlogArchive) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tFILENAME\tSTART TIME\tDURATION\tSTATUS")
	for _, a := range rows {
		start := a.CreatedAt
		if a.StartTime.Valid {
			start = a.StartTime.Int64
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			a.ID,
			a.Filename,
			formatUnixUTC(start),
			formatDuration(start, a.EndTime),
			a.Status,
		)
	}
	return tw.Flush()
}

func formatUnixUTC(sec int64) string {
	if sec <= 0 {
		return "-"
	}
	return time.Unix(sec, 0).UTC().Format(catalogTimeLayout)
}

func formatDuration(start int64, end sql.NullInt64) string {
	if start <= 0 || !end.Valid {
		return "-"
	}
	d := time.Duration(end.Int64-start) * time.Second
	if d < 0 {
		d = 0
	}
	return compactDuration(d)
}

func compactDuration(d time.Duration) string {
	sec := int64(d / time.Second)
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
