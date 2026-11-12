import os
import sys

# Ensure repository root is in sys.path
sys.path.insert(0, os.path.abspath("."))

import docx
from scripts.docx_helpers import init_document, add_cover_page
from scripts.content_overview import build_overview_chapters
from scripts.content_phases_1_to_10 import build_phases_1_to_10
from scripts.content_phases_11_to_20 import build_phases_11_to_20
from scripts.content_reference import build_reference_chapters

def generate_cookbook():
    print("[*] Initializing Word document with corporate executive styling...")
    doc = init_document()

    print("[*] Generating academic cover page (VESIT, Group 13, Dr. Priya R.L., Prathamesh Shelar, Chandrabhanu Prusty)...")
    add_cover_page(doc)

    print("[*] Assembling Executive Summary, System Architecture & Technology Stack...")
    build_overview_chapters(doc)

    print("[*] Assembling Implementation Phases 1 through 10...")
    build_phases_1_to_10(doc)

    print("[*] Assembling Implementation Phases 11 through 20...")
    build_phases_11_to_20(doc)

    print("[*] Assembling REST API Specification, Troubleshooting Manual, RTM & Viva Guide...")
    build_reference_chapters(doc)

    output_docx = os.path.abspath("LOCKGUARD_Cookbook_Final.docx")
    print(f"[*] Saving Word document to: {output_docx}")
    doc.save(output_docx)
    print(f"[+] DOCX successfully created: {os.path.getsize(output_docx):,} bytes")

    # Now export to PDF via Word COM automation
    output_pdf = os.path.abspath("LOCKGUARD_Cookbook_Final.pdf")
    print("[*] Initiating Microsoft Word COM automation to refresh fields and export PDF...")
    try:
        import win32com.client
        import pythoncom

        pythoncom.CoInitialize()
        word = win32com.client.Dispatch("Word.Application")
        word.Visible = False
        word.DisplayAlerts = False

        print(f"[*] Opening {output_docx} in Word...")
        doc_com = word.Documents.Open(output_docx)

        # Update all fields in document (including headers, footers, TOC)
        print("[*] Updating document fields and calculating page numbers...")
        try:
            for story in doc_com.StoryRanges:
                story.Fields.Update()
        except Exception as e:
            print(f"[!] Warning while updating story fields: {e}")

        # Compute page count
        page_count = doc_com.ComputeStatistics(2) # 2 = wdStatisticPages
        print(f"[+] Total document page count: {page_count} pages")

        print(f"[*] Exporting fixed format PDF to: {output_pdf}...")
        # 17 = wdFormatPDF
        doc_com.SaveAs(output_pdf, FileFormat=17)
        doc_com.Close(False)
        word.Quit()
        pythoncom.CoUninitialize()

        if os.path.exists(output_pdf):
            print(f"[+] PDF successfully created: {os.path.getsize(output_pdf):,} bytes ({page_count} pages)")
        else:
            print("[!] PDF file was not created by SaveAs.")
    except Exception as e:
        print(f"[!] Word COM automation error: {e}")
        print("[*] Document docx is preserved and complete.")

if __name__ == "__main__":
    generate_cookbook()
