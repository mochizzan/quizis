package handlers

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

// The XLSX export is a fixed three-sheet workbook; the frontend and tests
// address sheet and column names verbatim:
//
//	Murid         — per-student grid (No/Username/Nama/…/Q<i> pairs/Total Score)
//	Pertanyaan    — per-question analysis (full text, key, share, stats)
//	Rekapitulasi  — summary where EVERY numeric cell is a formula
//
// Layout rules: bold white header on the project primary with thin borders,
// frozen first row on every sheet, explicit column widths, right-aligned
// numerics ("0" counts, "0.00" scores/pcts), TOTAL rows bold with a
// distinct fill.
const (
	sheetMurid      = "Murid"
	sheetPertanyaan = "Pertanyaan"
	sheetRekap      = "Rekapitulasi"
	exportFillPrim  = "2C5EAD" // project primary (header row fill)
	exportFillTotal = "D9E2F3" // distinct TOTAL-row fill
	exportBorder    = "B7C4D6" // thin border color
)

// exportStyleIDs holds the styles created once per workbook.
type exportStyleIDs struct {
	header    int // bold white on primary, thin borders
	text      int // plain cell with thin borders
	num0      int // right-aligned count format "0" + borders
	num2      int // right-aligned score format "0.00" + borders
	section   int // bold section label
	totalTxt  int // TOTAL row: bold + distinct fill + borders
	totalNum0 int // TOTAL row count cell
	totalNum2 int // TOTAL row score cell
}

func newExportStyles(f *excelize.File) (exportStyleIDs, error) {
	thin := []excelize.Border{
		{Type: "left", Style: 1, Color: exportBorder},
		{Type: "right", Style: 1, Color: exportBorder},
		{Type: "top", Style: 1, Color: exportBorder},
		{Type: "bottom", Style: 1, Color: exportBorder},
	}
	right := &excelize.Alignment{Horizontal: "right"}
	primary := excelize.Fill{Type: "pattern", Color: []string{exportFillPrim}, Pattern: 1}
	totalFill := excelize.Fill{Type: "pattern", Color: []string{exportFillTotal}, Pattern: 1}

	var s exportStyleIDs
	var err error
	if s.header, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      primary,
		Border:    thin,
		Alignment: &excelize.Alignment{Vertical: "center"},
	}); err != nil {
		return s, err
	}
	if s.text, err = f.NewStyle(&excelize.Style{Border: thin}); err != nil {
		return s, err
	}
	if s.num0, err = f.NewStyle(&excelize.Style{
		NumFmt: 1, Border: thin, Alignment: right,
	}); err != nil {
		return s, err
	}
	if s.num2, err = f.NewStyle(&excelize.Style{
		NumFmt: 2, Border: thin, Alignment: right,
	}); err != nil {
		return s, err
	}
	if s.section, err = f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
	}); err != nil {
		return s, err
	}
	if s.totalTxt, err = f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true}, Fill: totalFill, Border: thin,
	}); err != nil {
		return s, err
	}
	if s.totalNum0, err = f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true}, Fill: totalFill, Border: thin,
		NumFmt: 1, Alignment: right,
	}); err != nil {
		return s, err
	}
	if s.totalNum2, err = f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true}, Fill: totalFill, Border: thin,
		NumFmt: 2, Alignment: right,
	}); err != nil {
		return s, err
	}
	return s, nil
}

// exportBook accumulates the first write error instead of aborting the
// layout halfway through.
type exportBook struct {
	f   *excelize.File
	st  exportStyleIDs
	err error
}

func (b *exportBook) fail(err error) {
	if b.err == nil && err != nil {
		b.err = err
	}
}

// colName converts a 1-based column number to its letter ("A", …).
func (b *exportBook) colName(col int) string {
	name, err := excelize.ColumnNumberToName(col)
	if err != nil {
		b.fail(err)
		return ""
	}
	return name
}

func (b *exportBook) axis(col, row int) string {
	name, err := excelize.CoordinatesToCellName(col, row)
	if err != nil {
		b.fail(err)
		return ""
	}
	return name
}

// str writes a literal string cell (static labels or values that are already
// injection-guarded — exportRows guards the identity columns).
func (b *exportBook) str(sheet string, col, row int, v string) {
	axis := b.axis(col, row)
	if axis == "" {
		return
	}
	b.fail(b.f.SetCellStr(sheet, axis, v))
}

// guarded writes user-derived text through guardValue + SetCellStr.
func (b *exportBook) guarded(sheet string, col, row int, v string) {
	b.str(sheet, col, row, guardValue(v))
}

// num writes a numeric cell (SetCellValue — never parsed as a formula).
func (b *exportBook) num(sheet string, col, row int, v float64) {
	axis := b.axis(col, row)
	if axis == "" {
		return
	}
	b.fail(b.f.SetCellValue(sheet, axis, v))
}

// formula writes a formula cell (Rekapitulasi metrics and TOTAL rows).
func (b *exportBook) formula(sheet string, col, row int, formula string) {
	axis := b.axis(col, row)
	if axis == "" {
		return
	}
	b.fail(b.f.SetCellFormula(sheet, axis, formula))
}

func (b *exportBook) styleAt(sheet string, col, row int, id int) {
	axis := b.axis(col, row)
	if axis == "" {
		return
	}
	b.fail(b.f.SetCellStyle(sheet, axis, axis, id))
}

func (b *exportBook) styleRange(sheet, hcell, vcell string, id int) {
	if hcell == "" || vcell == "" {
		return
	}
	b.fail(b.f.SetCellStyle(sheet, hcell, vcell, id))
}

func (b *exportBook) width(sheet, startCol, endCol string, w float64) {
	if startCol == "" {
		return
	}
	b.fail(b.f.SetColWidth(sheet, startCol, endCol, w))
}

// freeze pins the header row (ySplit=1) on the sheet.
func (b *exportBook) freeze(sheet string) {
	b.fail(b.f.SetPanes(sheet, &excelize.Panes{
		Freeze:      true,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
	}))
}

// buildExportWorkbook assembles the fixed 3-sheet workbook (the default
// "Sheet1" is dropped so exactly Murid, Pertanyaan, Rekapitulasi remain, in
// that order).
func buildExportWorkbook(quiz quizDetail, rows []exportRow, results []resultRow,
	questions []exportQuestion, stats []questionStat, answers answerCells) (*excelize.File, error) {
	f := excelize.NewFile()
	for _, name := range []string{sheetMurid, sheetPertanyaan, sheetRekap} {
		if _, err := f.NewSheet(name); err != nil {
			f.Close()
			return nil, err
		}
	}
	// activate Murid before dropping the default sheet
	f.SetActiveSheet(1)
	if err := f.DeleteSheet("Sheet1"); err != nil {
		f.Close()
		return nil, err
	}
	st, err := newExportStyles(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	b := &exportBook{f: f, st: st}
	b.murid(rows, results, questions, answers)
	b.pertanyaan(questions, stats)
	b.rekap(quiz, rows, questions)
	if b.err != nil {
		f.Close()
		return nil, b.err
	}
	return f, nil
}

// murid writes the student grid. Column order (row 1, verbatim contract):
// No | Username | Nama | Kelas | Jurusan | Status | Attempts | Cheating |
// Q1 Answer | Q1 Score | … | Total Score.
func (b *exportBook) murid(rows []exportRow, results []resultRow,
	questions []exportQuestion, answers answerCells) {
	sheet := sheetMurid
	headers := []string{"No", "Username", "Nama", "Kelas", "Jurusan",
		"Status", "Attempts", "Cheating"}
	for i := range questions {
		headers = append(headers, fmt.Sprintf("Q%d Answer", i+1), fmt.Sprintf("Q%d Score", i+1))
	}
	headers = append(headers, "Total Score")
	for i, h := range headers {
		b.str(sheet, i+1, 1, h)
	}

	for r, row := range rows {
		y := r + 2
		b.num(sheet, 1, y, float64(row.Rank)) // No = Rank (0 while unscored)
		b.str(sheet, 2, y, row.Username)
		b.str(sheet, 3, y, row.Name)
		b.str(sheet, 4, y, row.Class)
		b.str(sheet, 5, y, row.Major)
		b.str(sheet, 6, y, row.Status)
		b.num(sheet, 7, y, float64(row.Attempts))
		b.str(sheet, 8, y, row.Cheating)
		col := 9
		for _, q := range questions {
			a := answers.get(results[r].Username, q.Seq)
			if a.Text != "" {
				b.guarded(sheet, col, y, a.Text)
			}
			if a.Score != nil {
				b.num(sheet, col+1, y, *a.Score)
			}
			col += 2
		}
		// Total Score = numeric final else score_auto (exportRows
		// precedence); EMPTY when unscored — Sheet3's AVERAGE/COUNTIFS
		// ignore blanks, and a 0 here would poison them. (The CSV keeps
		// its legacy "0" for the same student — byte-compat contract.)
		if results[r].Final.Valid || results[r].Auto.Valid {
			b.num(sheet, col, y, row.Score)
		}
	}

	lastCol := b.colName(len(headers))
	b.freeze(sheet)
	if len(rows) > 0 && lastCol != "" {
		lastRow := fmt.Sprintf("%d", len(rows)+1)
		// borders over the whole table, then numeric formats on top
		b.styleRange(sheet, "A2", lastCol+lastRow, b.st.text)
		b.styleRange(sheet, "A2", "A"+lastRow, b.st.num0)
		b.styleRange(sheet, "G2", "G"+lastRow, b.st.num0)
		col := 9
		for range questions {
			sc := b.colName(col + 1)
			b.styleRange(sheet, sc+"2", sc+lastRow, b.st.num2)
			col += 2
		}
		b.styleRange(sheet, lastCol+"2", lastCol+lastRow, b.st.num2)
	}
	b.styleRange(sheet, "A1", lastCol+"1", b.st.header)

	// explicit widths: identity 6–26, Q columns capped ~12, Total 12
	b.width(sheet, "A", "A", 6)
	b.width(sheet, "B", "B", 14)
	b.width(sheet, "C", "C", 26)
	b.width(sheet, "D", "E", 16)
	b.width(sheet, "F", "F", 15)
	b.width(sheet, "G", "G", 10)
	b.width(sheet, "H", "H", 12)
	col := 9
	for range questions {
		b.width(sheet, b.colName(col), b.colName(col), 12)
		b.width(sheet, b.colName(col+1), b.colName(col+1), 10)
		col += 2
	}
	b.width(sheet, lastCol, lastCol, 12)
}

// pertanyaan writes the per-question analysis (row 1 verbatim contract):
// No | Teks | Tipe | Kunci | Nilai Maks | Benar | Salah | Tidak Dijawab |
// % Benar | % Salah | % Kosong | Paling Sering Dipilih — plus a bold TOTAL
// row whose =SUM of Nilai Maks equals the 100-point exam.
func (b *exportBook) pertanyaan(questions []exportQuestion, stats []questionStat) {
	sheet := sheetPertanyaan
	headers := []string{"No", "Teks", "Tipe", "Kunci", "Nilai Maks", "Benar",
		"Salah", "Tidak Dijawab", "% Benar", "% Salah", "% Kosong",
		"Paling Sering Dipilih"}
	for i, h := range headers {
		b.str(sheet, i+1, 1, h)
	}

	statBySeq := make(map[int]questionStat, len(stats))
	for _, s := range stats {
		statBySeq[s.Seq] = s
	}
	// equal weight: every question shares the 100-point exam (verified in
	// loadExportAnswers — score_auto = correct/total × 100)
	share := 0.0
	if len(questions) > 0 {
		share = 100 / float64(len(questions))
	}
	for i, q := range questions {
		y := i + 2
		s := statBySeq[q.Seq]
		b.num(sheet, 1, y, float64(q.Seq)) // No = the quiz's seq numbering
		b.guarded(sheet, 2, y, q.Text)     // FULL text, no 80-char truncation
		b.str(sheet, 3, y, q.Type)
		b.str(sheet, 4, y, q.Kunci)
		b.num(sheet, 5, y, share) // Nilai Maks — the question's share of 100
		b.num(sheet, 6, y, float64(s.Correct))
		b.num(sheet, 7, y, float64(s.Wrong))
		b.num(sheet, 8, y, float64(s.Unanswered))
		b.num(sheet, 9, y, s.CorrectPct)
		b.num(sheet, 10, y, s.WrongPct)
		b.num(sheet, 11, y, s.MissPct)
		b.str(sheet, 12, y, s.MostChosen)
	}

	b.freeze(sheet)
	if len(questions) > 0 {
		lastRow := len(questions) + 1
		totalRow := len(questions) + 2
		b.styleRange(sheet, "A2", "L"+fmt.Sprint(lastRow), b.st.text)
		b.styleRange(sheet, "A2", "A"+fmt.Sprint(lastRow), b.st.num0)
		b.styleRange(sheet, "E2", "E"+fmt.Sprint(lastRow), b.st.num2)
		b.styleRange(sheet, "F2", "H"+fmt.Sprint(lastRow), b.st.num0)
		b.styleRange(sheet, "I2", "K"+fmt.Sprint(lastRow), b.st.num2)
		// mandatory TOTAL row: bold + distinct fill, =SUM of the shares
		b.str(sheet, 1, totalRow, "TOTAL")
		b.formula(sheet, 5, totalRow, fmt.Sprintf("=SUM(E2:E%d)", lastRow))
		b.styleRange(sheet, "A"+fmt.Sprint(totalRow), "L"+fmt.Sprint(totalRow), b.st.totalTxt)
		b.styleRange(sheet, "E"+fmt.Sprint(totalRow), "E"+fmt.Sprint(totalRow), b.st.totalNum2)
	}
	b.styleRange(sheet, "A1", "L1", b.st.header)

	// widths: Teks generous (60), text columns 10–40, counts compact
	b.width(sheet, "A", "A", 6)
	b.width(sheet, "B", "B", 60)
	b.width(sheet, "C", "D", 10)
	b.width(sheet, "E", "E", 12)
	b.width(sheet, "F", "G", 8)
	b.width(sheet, "H", "H", 14)
	b.width(sheet, "I", "K", 9)
	b.width(sheet, "L", "L", 20)
}

// rekap writes the formula-only summary sheet. Static labels and section
// titles are text; EVERY numeric cell (Ringkasan values, distribution
// counts/percents, per-class figures, per-question figures) is a formula via
// SetCellFormula with cross-sheet references into Murid! and Pertanyaan!.
// Only the COUNTIF/AVERAGEIF/SUM/COUNTA/AVERAGE/MAX/MIN/COUNTIFS family is
// used — no MAXIFS or array formulas.
func (b *exportBook) rekap(quiz quizDetail, rows []exportRow, questions []exportQuestion) {
	sheet := sheetRekap

	// Formula ranges span rows 2..n+1 (row 1 is the header). With zero
	// students the range collapses to the empty first data row so the
	// COUNTA/COUNTIF formulas still evaluate to 0.
	n := len(rows)
	if n < 1 {
		n = 1
	}
	last := n + 1
	totalCol := b.colName(9 + 2*len(questions)) // Murid's Total Score column
	rng := func(col string) string {
		return fmt.Sprintf("Murid!$%s$2:$%s$%d", col, col, last)
	}

	b.str(sheet, 1, 1, fmt.Sprintf("Rekapitulasi — %s (%s)", quiz.Judul, quiz.Code))

	var num0, num2 [][2]int // (col, row) pairs to style after the base pass
	var sectionRows []int
	section := func(row int, label string) {
		b.str(sheet, 1, row, label)
		sectionRows = append(sectionRows, row)
	}

	row := 3
	section(row, "Ringkasan")
	row++
	jumlahRow := row
	metrics := []struct{ label, formula string }{
		{"Jumlah peserta", fmt.Sprintf("=COUNTA(%s)", rng("B"))},
		{"Selesai", fmt.Sprintf(`=COUNTIF(%s,"finished")`, rng("F"))},
		{"Dihapus", fmt.Sprintf(`=COUNTIF(%s,"removed")`, rng("F"))},
		{"Belum mengerjakan", fmt.Sprintf(`=COUNTIF(%s,"not-attempted")`, rng("F"))},
		{"Rata-rata", fmt.Sprintf("=AVERAGE(%s)", rng(totalCol))},
		{"Tertinggi", fmt.Sprintf("=MAX(%s)", rng(totalCol))},
		{"Terendah", fmt.Sprintf("=MIN(%s)", rng(totalCol))},
	}
	for i, m := range metrics {
		b.str(sheet, 1, row, m.label)
		b.formula(sheet, 2, row, m.formula)
		if i < 4 {
			num0 = append(num0, [2]int{2, row})
		} else {
			num2 = append(num2, [2]int{2, row})
		}
		row++
	}
	row++ // blank spacer

	section(row, "Distribusi nilai")
	row++
	bins := []struct {
		label string
		lo    int
		hi    int
	}{
		{"0–19", 0, 19}, {"20–39", 20, 39}, {"40–59", 40, 59},
		{"60–79", 60, 79}, {"80–100", 80, 100},
	}
	for _, bin := range bins {
		b.str(sheet, 1, row, bin.label)
		b.formula(sheet, 2, row, fmt.Sprintf(`=COUNTIFS(%s,">=%d",%s,"<=%d")`,
			rng(totalCol), bin.lo, rng(totalCol), bin.hi))
		// percent of Jumlah peserta — formula-divided, never a static fraction
		b.formula(sheet, 3, row, fmt.Sprintf(`=IFERROR(B%d/$B$%d*100,"")`, row, jumlahRow))
		num0 = append(num0, [2]int{2, row})
		num2 = append(num2, [2]int{3, row})
		row++
	}
	row++ // blank spacer

	section(row, "Rekap per kelas")
	row++
	seen := make(map[string]bool)
	for _, r := range rows {
		// blank classes carry no label cell to match against (COUNTIF with
		// an empty criteria cell resolves to 0, not "") — distinct NON-blank
		// classes only; unclassed students still count in Ringkasan.
		if r.Class == "" || seen[r.Class] {
			continue
		}
		seen[r.Class] = true
		b.str(sheet, 1, row, r.Class) // static label (already guarded)
		b.formula(sheet, 2, row, fmt.Sprintf("=COUNTIF(%s,A%d)", rng("D"), row))
		b.formula(sheet, 3, row, fmt.Sprintf("=AVERAGEIF(%s,A%d,%s)",
			rng("D"), row, rng(totalCol)))
		num0 = append(num0, [2]int{2, row})
		num2 = append(num2, [2]int{3, row})
		row++
	}
	row++ // blank spacer

	section(row, "Rekap per pertanyaan")
	row++
	for i := range questions {
		qrow := i + 2 // the question's row on Pertanyaan
		b.formula(sheet, 1, row, fmt.Sprintf("=Pertanyaan!B%d", qrow))
		b.formula(sheet, 2, row, fmt.Sprintf("=Pertanyaan!F%d", qrow))
		scoreCol := b.colName(10 + 2*i) // Q<i+1> Score column on Murid
		b.formula(sheet, 3, row, fmt.Sprintf(`=IFERROR(AVERAGE(%s),"—")`, rng(scoreCol)))
		b.formula(sheet, 4, row, fmt.Sprintf("=Pertanyaan!I%d", qrow))
		num0 = append(num0, [2]int{2, row})
		num2 = append(num2, [2]int{3, row}, [2]int{4, row})
		row++
	}
	lastUsed := row - 1

	// borders over the used area first, then numeric formats, then the
	// bolder labels (SetCellStyle replaces, so order matters).
	if lastUsed >= 1 {
		b.styleRange(sheet, "A1", "D"+fmt.Sprint(lastUsed), b.st.text)
	}
	for _, c := range num0 {
		b.styleAt(sheet, c[0], c[1], b.st.num0)
	}
	for _, c := range num2 {
		b.styleAt(sheet, c[0], c[1], b.st.num2)
	}
	for _, r := range sectionRows {
		b.styleAt(sheet, 1, r, b.st.section)
	}
	b.styleRange(sheet, "A1", "D1", b.st.header) // styled title row

	b.freeze(sheet)
	b.width(sheet, "A", "A", 36)
	b.width(sheet, "B", "C", 18)
	b.width(sheet, "D", "D", 18)
}
