package main

import (
	"fmt"
	"goprint/api"
	"os"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
)

func testCopiesPreview() {
	sourceFile := "printer_test_3.pdf"
	copies := 2

	fmt.Printf("Testing reverse + no-collate preview...\n")
	fmt.Printf("Source: %s, Copies: %d\n", sourceFile, copies)

	pageCount, err := pdfapi.PageCountFile(sourceFile)
	if err != nil {
		fmt.Printf("Error reading source page count: %v\n", err)
		return
	}
	fmt.Printf("Source page count: %d\n", pageCount)

	reversedPath, err := api.ApplySingleSideReverse(sourceFile)
	if err != nil {
		fmt.Printf("Error in ApplySingleSideReverse: %v\n", err)
		return
	}
	defer func() {
		if reversedPath != sourceFile {
			_ = os.Remove(reversedPath)
		}
	}()

	result, err := api.ApplyUncollatedCopies(reversedPath, copies)
	if err != nil {
		fmt.Printf("Error in ApplyUncollatedCopies: %v\n", err)
		return
	}
	defer func() {
		if result != reversedPath {
			_ = os.Remove(result)
		}
	}()

	fmt.Printf("Generated preview PDF: %s\n", result)

	previewPageCount, err := pdfapi.PageCountFile(result)
	if err != nil {
		fmt.Printf("Error reading preview page count: %v\n", err)
		return
	}
	fmt.Printf("Preview page count: %d (expected %d)\n", previewPageCount, pageCount*copies)

	persistentPath, err := api.SavePDFForTest(result, "reverse-no-collate-preview")
	if err != nil {
		fmt.Printf("Error saving preview PDF: %v\n", err)
		return
	}
	fmt.Printf("Saved preview PDF to: %s\n", persistentPath)
	fmt.Println("Preview generation passed!")
}

func main() {
	testCopiesPreview()
}
