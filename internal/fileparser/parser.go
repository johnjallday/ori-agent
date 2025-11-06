package fileparser

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"
)

// ParsePDF extracts text content from a PDF file
func ParsePDF(data []byte) (string, error) {
	reader := bytes.NewReader(data)
	pdfReader, err := pdf.NewReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to create PDF reader: %w", err)
	}

	var text strings.Builder
	numPages := pdfReader.NumPage()

	for pageNum := 1; pageNum <= numPages; pageNum++ {
		page := pdfReader.Page(pageNum)
		if page.V.IsNull() {
			continue
		}

		pageText, err := page.GetPlainText(nil)
		if err != nil {
			// Log error but continue with other pages
			text.WriteString(fmt.Sprintf("[Error reading page %d: %v]\n", pageNum, err))
			continue
		}

		text.WriteString(fmt.Sprintf("=== Page %d ===\n", pageNum))
		text.WriteString(pageText)
		text.WriteString("\n\n")
	}

	return text.String(), nil
}

// WordDocument represents a simple DOCX document structure
type WordDocument struct {
	XMLName xml.Name `xml:"document"`
	Body    WordBody `xml:"body"`
}

type WordBody struct {
	Paragraphs []WordParagraph `xml:"p"`
}

type WordParagraph struct {
	Texts []WordText `xml:"r>t"`
}

type WordText struct {
	Value string `xml:",chardata"`
}

// ParseDOCX extracts text content from a DOCX file
func ParseDOCX(data []byte) (string, error) {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to read DOCX as zip: %w", err)
	}

	// Find document.xml
	var documentXML *zip.File
	for _, file := range zipReader.File {
		if file.Name == "word/document.xml" {
			documentXML = file
			break
		}
	}

	if documentXML == nil {
		return "", fmt.Errorf("document.xml not found in DOCX file")
	}

	// Read document.xml
	rc, err := documentXML.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open document.xml: %w", err)
	}
	defer rc.Close()

	// Parse XML
	var doc WordDocument
	decoder := xml.NewDecoder(rc)
	if err := decoder.Decode(&doc); err != nil {
		return "", fmt.Errorf("failed to parse document.xml: %w", err)
	}

	// Extract text
	var text strings.Builder
	for _, paragraph := range doc.Body.Paragraphs {
		for _, textNode := range paragraph.Texts {
			text.WriteString(textNode.Value)
		}
		text.WriteString("\n")
	}

	return text.String(), nil
}

// ParseFile parses a file based on its type and returns the text content
func ParseFile(filename string, data []byte) (string, error) {
	lower := strings.ToLower(filename)

	if strings.HasSuffix(lower, ".pdf") {
		return ParsePDF(data)
	} else if strings.HasSuffix(lower, ".docx") {
		return ParseDOCX(data)
	} else if strings.HasSuffix(lower, ".txt") ||
		strings.HasSuffix(lower, ".md") ||
		strings.HasSuffix(lower, ".json") ||
		strings.HasSuffix(lower, ".xml") ||
		strings.HasSuffix(lower, ".html") ||
		strings.HasSuffix(lower, ".csv") {
		// Plain text files - just return as string
		return string(data), nil
	}

	return "", fmt.Errorf("unsupported file type: %s", filename)
}

// ValidateFileSize checks if file size is within limits (10MB)
func ValidateFileSize(size int64) error {
	const maxSize = 10 * 1024 * 1024 // 10MB
	if size > maxSize {
		return fmt.Errorf("file size %d bytes exceeds maximum allowed size of %d bytes", size, maxSize)
	}
	return nil
}
