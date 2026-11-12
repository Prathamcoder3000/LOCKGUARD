import os
import docx
from docx.shared import Inches, Pt, RGBColor
from docx.enum.text import WD_ALIGN_PARAGRAPH
from docx.enum.table import WD_TABLE_ALIGNMENT
from docx.oxml import OxmlElement, parse_xml
from docx.oxml.ns import nsdecls, qn

# Palette constants
COLOR_PRIMARY = RGBColor(11, 19, 43)      # #0B132B Dark Navy
COLOR_SECONDARY = RGBColor(30, 58, 138)   # #1E3A8A Blue
COLOR_ACCENT = RGBColor(37, 99, 235)      # #2563EB Bright Blue
COLOR_TEXT = RGBColor(15, 23, 42)         # #0F172A Slate 900
COLOR_MUTED = RGBColor(100, 116, 139)     # #64748B Slate 500
COLOR_CODE = RGBColor(15, 23, 42)         # #0F172A Dark

HEX_PRIMARY = "0B132B"
HEX_SECONDARY = "1E3A8A"
HEX_ACCENT = "2563EB"
HEX_BG_LIGHT = "F8FAFC"
HEX_BORDER = "CBD5E1"
HEX_ZEBRA = "F1F5F9"

def init_document():
    doc = docx.Document()

    # Set page margins: 1 inch on all sides
    for section in doc.sections:
        section.top_margin = Inches(1.0)
        section.bottom_margin = Inches(1.0)
        section.left_margin = Inches(1.0)
        section.right_margin = Inches(1.0)
        section.different_first_page_header_footer = True

        # Header
        header = section.header
        hp = header.paragraphs[0]
        hp.alignment = WD_ALIGN_PARAGRAPH.RIGHT
        hrun = hp.add_run("LOCKGUARD — Fault Tolerant Distributed Lock Service  |  Technical Cookbook")
        hrun.font.name = "Calibri"
        hrun.font.size = Pt(8.5)
        hrun.font.color.rgb = COLOR_MUTED

        # Footer
        footer = section.footer
        fp = footer.paragraphs[0]
        fp.alignment = WD_ALIGN_PARAGRAPH.JUSTIFY
        frun = fp.add_run("VEERMATA JIJABAI TECHNOLOGICAL INSTITUTE (VESIT)  •  Software Design (Group 13)")
        frun.font.name = "Calibri"
        frun.font.size = Pt(8.5)
        frun.font.color.rgb = COLOR_MUTED

    # Configure Normal Style
    normal_style = doc.styles['Normal']
    normal_style.font.name = 'Calibri'
    normal_style.font.size = Pt(10.5)
    normal_style.font.color.rgb = COLOR_TEXT
    normal_style.paragraph_format.line_spacing = 1.15
    normal_style.paragraph_format.space_after = Pt(4)

    return doc

def add_heading_1(doc, text):
    p = doc.add_paragraph()
    p.paragraph_format.space_before = Pt(16)
    p.paragraph_format.space_after = Pt(6)
    p.paragraph_format.keep_with_next = True
    run = p.add_run(text)
    run.font.name = 'Calibri'
    run.font.size = Pt(17)
    run.font.bold = True
    run.font.color.rgb = COLOR_PRIMARY
    return p

def add_heading_2(doc, text):
    p = doc.add_paragraph()
    p.paragraph_format.space_before = Pt(12)
    p.paragraph_format.space_after = Pt(4)
    p.paragraph_format.keep_with_next = True
    run = p.add_run(text)
    run.font.name = 'Calibri'
    run.font.size = Pt(13)
    run.font.bold = True
    run.font.color.rgb = COLOR_SECONDARY
    return p

def add_heading_3(doc, text):
    p = doc.add_paragraph()
    p.paragraph_format.space_before = Pt(8)
    p.paragraph_format.space_after = Pt(3)
    p.paragraph_format.keep_with_next = True
    run = p.add_run(text)
    run.font.name = 'Calibri'
    run.font.size = Pt(11)
    run.font.bold = True
    run.font.color.rgb = COLOR_ACCENT
    return p

def add_paragraph(doc, text, bold_prefix=None, italic=False):
    p = doc.add_paragraph()
    p.paragraph_format.space_after = Pt(4)
    p.paragraph_format.line_spacing = 1.15
    if bold_prefix:
        r_pre = p.add_run(bold_prefix)
        r_pre.font.bold = True
        r_pre.font.color.rgb = COLOR_PRIMARY
    r = p.add_run(text)
    if italic:
        r.font.italic = True
    return p

def add_bullet(doc, text, bold_prefix=None):
    p = doc.add_paragraph(style='List Bullet')
    p.paragraph_format.space_after = Pt(2)
    p.paragraph_format.line_spacing = 1.15
    if bold_prefix:
        r_pre = p.add_run(bold_prefix)
        r_pre.font.bold = True
        r_pre.font.color.rgb = COLOR_PRIMARY
    p.add_run(text)
    return p

def add_callout(doc, text, alert_type="NOTE"):
    type_configs = {
        "NOTE": {"color": "2563EB", "bg": "EFF6FF", "title": "NOTE"},
        "IMPORTANT": {"color": "1E3A8A", "bg": "EEF2FF", "title": "IMPORTANT INVARIANT"},
        "WARNING": {"color": "D97706", "bg": "FFFBEB", "title": "SECURITY WARNING"},
        "CAUTION": {"color": "DC2626", "bg": "FEF2F2", "title": "CAUTION & SAFETY PRINCIPLE"},
    }
    cfg = type_configs.get(alert_type, type_configs["NOTE"])

    table = doc.add_table(rows=1, cols=1)
    table.alignment = WD_TABLE_ALIGNMENT.CENTER
    table.autofit = False
    cell = table.cell(0, 0)
    cell.width = Inches(6.5)

    # Shading and left border
    shd = parse_xml(f'<w:shd {nsdecls("w")} w:fill="{cfg["bg"]}"/>')
    cell._tc.get_or_add_tcPr().append(shd)

    borders = parse_xml(f"""
        <w:tcBorders {nsdecls("w")}>
            <w:top w:val="none"/>
            <w:left w:val="single" w:sz="24" w:space="0" w:color="{cfg['color']}"/>
            <w:bottom w:val="none"/>
            <w:right w:val="none"/>
        </w:tcBorders>
    """)
    cell._tc.get_or_add_tcPr().append(borders)

    margins = parse_xml(f"""
        <w:tcMar {nsdecls("w")}>
            <w:top w:w="120" w:type="dxa"/>
            <w:bottom w:w="120" w:type="dxa"/>
            <w:left w:w="180" w:type="dxa"/>
            <w:right w:w="180" w:type="dxa"/>
        </w:tcMar>
    """)
    cell._tc.get_or_add_tcPr().append(margins)

    p = cell.paragraphs[0]
    p.paragraph_format.space_after = Pt(0)
    p.paragraph_format.line_spacing = 1.15
    run_hdr = p.add_run(f"[{cfg['title']}] ")
    run_hdr.font.name = "Calibri"
    run_hdr.font.bold = True
    run_hdr.font.size = Pt(10)
    run_hdr.font.color.rgb = RGBColor(int(cfg['color'][:2], 16), int(cfg['color'][2:4], 16), int(cfg['color'][4:], 16))

    run_text = p.add_run(text)
    run_text.font.name = "Calibri"
    run_text.font.size = Pt(10)
    run_text.font.color.rgb = COLOR_TEXT

    p_after = doc.add_paragraph()
    p_after.paragraph_format.space_before = Pt(4)
    p_after.paragraph_format.space_after = Pt(2)

def add_code_block(doc, code, filepath=None):
    table = doc.add_table(rows=1, cols=1)
    table.alignment = WD_TABLE_ALIGNMENT.CENTER
    table.autofit = False
    cell = table.cell(0, 0)
    cell.width = Inches(6.5)

    shd = parse_xml(f'<w:shd {nsdecls("w")} w:fill="{HEX_BG_LIGHT}"/>')
    cell._tc.get_or_add_tcPr().append(shd)

    borders = parse_xml(f"""
        <w:tcBorders {nsdecls("w")}>
            <w:top w:val="single" w:sz="6" w:space="0" w:color="{HEX_BORDER}"/>
            <w:left w:val="single" w:sz="16" w:space="0" w:color="{HEX_SECONDARY}"/>
            <w:bottom w:val="single" w:sz="6" w:space="0" w:color="{HEX_BORDER}"/>
            <w:right w:val="single" w:sz="6" w:space="0" w:color="{HEX_BORDER}"/>
        </w:tcBorders>
    """)
    cell._tc.get_or_add_tcPr().append(borders)

    margins = parse_xml(f"""
        <w:tcMar {nsdecls("w")}>
            <w:top w:w="100" w:type="dxa"/>
            <w:bottom w:w="100" w:type="dxa"/>
            <w:left w:w="150" w:type="dxa"/>
            <w:right w:w="150" w:type="dxa"/>
        </w:tcMar>
    """)
    cell._tc.get_or_add_tcPr().append(margins)

    p = cell.paragraphs[0]
    p.paragraph_format.space_after = Pt(2)
    p.paragraph_format.line_spacing = 1.05

    if filepath:
        run_fp = p.add_run(f"// File: {filepath}\n")
        run_fp.font.name = "Consolas"
        run_fp.font.size = Pt(8.5)
        run_fp.font.bold = True
        run_fp.font.color.rgb = COLOR_SECONDARY

    run_code = p.add_run(code.strip())
    run_code.font.name = "Consolas"
    run_code.font.size = Pt(9.0)
    run_code.font.color.rgb = COLOR_CODE

    p_after = doc.add_paragraph()
    p_after.paragraph_format.space_before = Pt(3)
    p_after.paragraph_format.space_after = Pt(2)

def add_figure(doc, img_rel_path, caption, explanation=None, width_inches=6.0):
    full_path = os.path.abspath(img_rel_path)
    if not os.path.exists(full_path):
        p_err = doc.add_paragraph()
        p_err.add_run(f"[FIGURE NOT FOUND: {img_rel_path}]").font.color.rgb = RGBColor(220, 38, 38)
        return

    p_img = doc.add_paragraph()
    p_img.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p_img.paragraph_format.space_before = Pt(8)
    p_img.paragraph_format.space_after = Pt(2)
    p_img.paragraph_format.keep_with_next = True
    run_img = p_img.add_run()
    run_img.add_picture(full_path, width=Inches(width_inches))

    p_cap = doc.add_paragraph()
    p_cap.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p_cap.paragraph_format.space_before = Pt(2)
    p_cap.paragraph_format.space_after = Pt(6)
    p_cap.paragraph_format.keep_with_next = bool(explanation)
    run_cap = p_cap.add_run(caption)
    run_cap.font.name = "Calibri"
    run_cap.font.bold = True
    run_cap.font.size = Pt(9.5)
    run_cap.font.color.rgb = COLOR_PRIMARY

    if explanation:
        p_exp = doc.add_paragraph()
        p_exp.paragraph_format.space_after = Pt(6)
        run_exp = p_exp.add_run(explanation)
        run_exp.font.name = "Calibri"
        run_exp.font.size = Pt(9.5)
        run_exp.font.italic = True
        run_exp.font.color.rgb = COLOR_MUTED

def add_table(doc, headers, rows, col_widths=None):
    table = doc.add_table(rows=len(rows) + 1, cols=len(headers))
    table.alignment = WD_TABLE_ALIGNMENT.CENTER
    table.autofit = False

    # Format header row
    hdr_cells = table.rows[0].cells
    for i, title in enumerate(headers):
        cell = hdr_cells[i]
        shd = parse_xml(f'<w:shd {nsdecls("w")} w:fill="{HEX_PRIMARY}"/>')
        cell._tc.get_or_add_tcPr().append(shd)

        borders = parse_xml(f"""
            <w:tcBorders {nsdecls("w")}>
                <w:top w:val="single" w:sz="6" w:space="0" w:color="{HEX_PRIMARY}"/>
                <w:left w:val="none"/>
                <w:bottom w:val="single" w:sz="12" w:space="0" w:color="{HEX_SECONDARY}"/>
                <w:right w:val="none"/>
            </w:tcBorders>
        """)
        cell._tc.get_or_add_tcPr().append(borders)

        margins = parse_xml(f"""
            <w:tcMar {nsdecls("w")}>
                <w:top w:w="80" w:type="dxa"/>
                <w:bottom w:w="80" w:type="dxa"/>
                <w:left w:w="120" w:type="dxa"/>
                <w:right w:w="120" w:type="dxa"/>
            </w:tcMar>
        """)
        cell._tc.get_or_add_tcPr().append(margins)

        p = cell.paragraphs[0]
        p.paragraph_format.space_after = Pt(0)
        p.paragraph_format.line_spacing = 1.05
        run = p.add_run(title)
        run.font.name = "Calibri"
        run.font.bold = True
        run.font.size = Pt(9.5)
        run.font.color.rgb = RGBColor(255, 255, 255)

    # Repeat header row on new pages
    trPr = table.rows[0]._tr.get_or_add_trPr()
    trPr.append(parse_xml(f'<w:tblHeader {nsdecls("w")}/>'))

    # Format data rows
    for r_idx, row in enumerate(rows):
        row_cells = table.rows[r_idx + 1].cells
        bg_fill = HEX_ZEBRA if r_idx % 2 == 1 else "FFFFFF"

        # Prevent row split across pages
        r_trPr = table.rows[r_idx + 1]._tr.get_or_add_trPr()
        r_trPr.append(parse_xml(f'<w:cantSplit {nsdecls("w")}/>'))

        for c_idx, val in enumerate(row):
            cell = row_cells[c_idx]
            shd = parse_xml(f'<w:shd {nsdecls("w")} w:fill="{bg_fill}"/>')
            cell._tc.get_or_add_tcPr().append(shd)

            borders = parse_xml(f"""
                <w:tcBorders {nsdecls("w")}>
                    <w:top w:val="single" w:sz="4" w:space="0" w:color="{HEX_BORDER}"/>
                    <w:left w:val="none"/>
                    <w:bottom w:val="single" w:sz="4" w:space="0" w:color="{HEX_BORDER}"/>
                    <w:right w:val="none"/>
                </w:tcBorders>
            """)
            cell._tc.get_or_add_tcPr().append(borders)

            margins = parse_xml(f"""
                <w:tcMar {nsdecls("w")}>
                    <w:top w:w="60" w:type="dxa"/>
                    <w:bottom w:w="60" w:type="dxa"/>
                    <w:left w:w="100" w:type="dxa"/>
                    <w:right w:w="100" w:type="dxa"/>
                </w:tcMar>
            """)
            cell._tc.get_or_add_tcPr().append(margins)

            p = cell.paragraphs[0]
            p.paragraph_format.space_after = Pt(0)
            p.paragraph_format.line_spacing = 1.05
            run = p.add_run(str(val))
            run.font.name = "Calibri"
            run.font.size = Pt(9.0)
            run.font.color.rgb = COLOR_TEXT

    # Set column widths if provided
    if col_widths:
        for r in table.rows:
            for i, w in enumerate(col_widths):
                r.cells[i].width = Inches(w)

    p_after = doc.add_paragraph()
    p_after.paragraph_format.space_before = Pt(3)
    p_after.paragraph_format.space_after = Pt(2)

def add_cover_page(doc):
    p_top = doc.add_paragraph()
    p_top.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p_top.paragraph_format.space_before = Pt(36)
    p_top.paragraph_format.space_after = Pt(4)
    run_inst = p_top.add_run("VEERMATA JIJABAI TECHNOLOGICAL INSTITUTE (VESIT)\n")
    run_inst.font.name = "Calibri"
    run_inst.font.size = Pt(16)
    run_inst.font.bold = True
    run_inst.font.color.rgb = COLOR_PRIMARY

    run_dept = p_top.add_run("Department of Computer Engineering\n")
    run_dept.font.name = "Calibri"
    run_dept.font.size = Pt(13)
    run_dept.font.color.rgb = COLOR_SECONDARY

    run_line = p_top.add_run("―" * 32)
    run_line.font.color.rgb = COLOR_MUTED

    # Title
    p_title = doc.add_paragraph()
    p_title.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p_title.paragraph_format.space_before = Pt(30)
    p_title.paragraph_format.space_after = Pt(8)
    run_t1 = p_title.add_run("LOCKGUARD\n")
    run_t1.font.name = "Calibri"
    run_t1.font.size = Pt(32)
    run_t1.font.bold = True
    run_t1.font.color.rgb = COLOR_PRIMARY

    run_sub = p_title.add_run("Fault Tolerant Distributed Lock Service\n")
    run_sub.font.name = "Calibri"
    run_sub.font.size = Pt(18)
    run_sub.font.bold = True
    run_sub.font.color.rgb = COLOR_ACCENT

    run_desc = p_title.add_run("Comprehensive Technical Cookbook & Operational Engineering Manual\n")
    run_desc.font.name = "Calibri"
    run_desc.font.size = Pt(12)
    run_desc.font.color.rgb = COLOR_MUTED

    # Course info
    p_course = doc.add_paragraph()
    p_course.alignment = WD_ALIGN_PARAGRAPH.CENTER
    p_course.paragraph_format.space_before = Pt(20)
    p_course.paragraph_format.space_after = Pt(24)
    r_c = p_course.add_run("Software Design (SD)  •  T.E. Computer Engineering  •  Semester V\nAcademic Year 2026–2027  •  Group 13")
    r_c.font.name = "Calibri"
    r_c.font.size = Pt(11)
    r_c.font.bold = True
    r_c.font.color.rgb = COLOR_SECONDARY

    # Authors Table
    table = doc.add_table(rows=3, cols=2)
    table.alignment = WD_TABLE_ALIGNMENT.CENTER
    table.autofit = False

    col_widths = [3.2, 3.2]
    headers = ["SUBMITTED BY (STUDENTS)", "SUBMITTED TO (FACULTY)"]
    for i, h in enumerate(headers):
        c = table.cell(0, i)
        c.width = Inches(col_widths[i])
        shd = parse_xml(f'<w:shd {nsdecls("w")} w:fill="{HEX_PRIMARY}"/>')
        c._tc.get_or_add_tcPr().append(shd)
        p = c.paragraphs[0]
        r = p.add_run(h)
        r.font.bold = True
        r.font.size = Pt(10)
        r.font.color.rgb = RGBColor(255, 255, 255)

    # Row 1
    c01 = table.cell(1, 0)
    p01 = c01.paragraphs[0]
    r = p01.add_run("Prathamesh Shelar\n")
    r.font.bold = True
    p01.add_run("Roll No. 65  |  Class D12A\nd2025.prathamesh.shelar@ves.ac.in\n\n")
    r2 = p01.add_run("Chandrabhanu Prusty\n")
    r2.font.bold = True
    p01.add_run("Roll No. 48  |  Class D12A\nd2024.chandrabhanu.prusty@ves.ac.in")

    c02 = table.cell(1, 1)
    p02 = c02.paragraphs[0]
    r = p02.add_run("Faculty Guide:\n")
    r.font.bold = True
    r_name = p02.add_run("Dr. Priya R.L.\n")
    r_name.font.bold = True
    r_name.font.color.rgb = COLOR_SECONDARY
    p02.add_run("Department of Computer Engineering\nCourse: Software Design\nGroup: 13")

    # Row 2: Date
    table.cell(2, 0).paragraphs[0].add_run("Submission Date: 12 November 2026").font.bold = True
    table.cell(2, 1).paragraphs[0].add_run("Status: Final University Project Submission").font.bold = True

    doc.add_page_break()
