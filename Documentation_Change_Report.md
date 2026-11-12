# LOCKGUARD — Documentation Change & Transformation Report

**Project Title:** Fault Tolerant Distributed Lock Service (LOCKGUARD)  
**Document Transformed:** `LOCKGUARD_Cookbook_Final_Professional.docx` -> `LOCKGUARD_Cookbook_Final.docx`  
**Date:** 10 November 2026  
**Authors:** Prathamesh Shelar, Chandrabhanu Prusty  
**Supervisor:** Dr. Priya R.L.  

---

## 1. Overview of Changes

The original draft cookbook provided an initial high-level outline and theoretical overview of the project. However, it lacked:
1. Actual source code listings with detailed line-by-line engineering explanations.
2. Real terminal execution evidence and genuine UI screenshots from the running application.
3. Accurate published host ports reflecting the active Docker Compose deployment.
4. Dedicated practical procedure sections for each distributed systems phase.
5. High-resolution architecture and sequence diagrams.
6. A comprehensive Requirement Traceability Matrix and Troubleshooting guide.

This transformation upgrades the draft into an exhaustive, 60+ page, publication-grade academic technical manual suitable for university submission.

---

## 2. Detailed Summary of Additions & Enhancements

| Area / Chapter | Original Draft Status | Final Master Cookbook Transformation |
| :--- | :--- | :--- |
| **Institutional Header & Control** | Basic tabular text | Standardized VJTI / VESIT department header, academic group details, course metadata, and formal document control table. |
| **Host Ports & Service Wiring** | Ambiguous ports (mentioned 8080 and 3000) | Clarified root `docker-compose.yml` authoritative mappings: Nginx (80), API (8082), PostgreSQL (5435), Protected Resource (9000), Prometheus (9090). |
| **Source Code Inclusion** | Minimal or none | Embedded actual repository source code across 8 core subsystems (API server, JWT auth, Raft node, Lock FSM, Protected Resource, Audit pipeline, React UI, Docker/Nginx configs). |
| **Code Explanations** | None | Added comprehensive line-by-line walkthroughs explaining data structures, invariants, algorithms, and inter-component interactions. |
| **Practical Execution Procedures** | High-level checklists | Structured all 20 phases into standardized practical units: Aim, Requirements, Concept, Components, Step-by-Step Procedure, Commands, Code, Output, Screenshots, Verification, Observations, Errors & Solutions, Conclusion. |
| **Screenshot Evidence** | Placeholders only | Captured and embedded 13 genuine Chrome browser screenshots of the live dashboard (`fig_01` to `fig_13`) and 16 terminal execution captures (`term_01` to `term_16`). |
| **Architecture Diagrams** | Textual Mermaid code | Rendered all 10 Mermaid architecture and sequence diagrams into high-resolution PNG images (`diag_01` to `diag_10`) embedded with descriptive captions. |
| **API Reference** | Summary table | Exhaustive REST API specification with complete request/response JSON bodies, RFC 3339 timestamps, error envelopes, and HTTP status codes. |
| **Testing & Verification** | General guidance | Detailed test matrix covering 56 backend tests, 8 protected resource tests, consensus stress suites, static analysis, and frontend build outputs. |
| **Requirement Traceability Matrix** | Absent | Added full traceability matrix linking all functional and non-functional requirements to source code files, API routes, test cases, and evidence figures. |
| **Troubleshooting Guide** | Generic list | Comprehensive 18-issue diagnostic matrix covering Docker, Raft consensus, database, fencing, and Windows environment issues with exact diagnostic and corrective commands. |
| **Viva Voce Examination Guide** | Basic outline | Expanded into an 80-question academic oral defense guide divided into 50 technical Q&As, 20 architectural explanations, and 10 complex failure scenarios. |

---

## 3. Specific Technical Corrections

1. **API Gateway Route Mappings:** Corrected routes to document both `/v1/...` and `/api/v1/...` path registrations, explaining how Nginx path stripping operates.
2. **PostgreSQL Audit Semantics:** Clarified that PostgreSQL operates strictly as an asynchronous, best-effort audit store and is never the authoritative lock-state authority.
3. **Fencing Token Invariant:** Formally documented that fencing tokens must strictly increment on every acquisition ($\tau \leftarrow \tau + 1$) and remain preserved during renewals, while downstream storage rejects stale tokens ($\tau \le \tau_{\text{highest}}$).
4. **Lease Versioning:** Corrected lease renewal rules to require exact $+1$ version progression ($V_{\text{new}} = V_{\text{old}} + 1$).
5. **Secret Masking:** Documented the security controls in `SettingsPage.tsx` that mask database passwords and JWT secrets.

---

## 4. Document Formatting Improvements

- **Palette:** Implemented a unified academic palette: Primary Navy (`#0B132B`), Deep Accent (`#1E3A8A`), Link Blue (`#2563EB`), Slate Gray (`#475569`), Dark Monospace (`#0F172A`).
- **Typography:** Standardized on `Calibri` / `Arial` for clear headings and prose, and `Consolas` / `JetBrains Mono` for code and terminal captures.
- **Tables:** Applied dark-shaded headers with bold white text, alternating subtle zebra rows, and explicit column width formatting.
- **Code Blocks:** Styled with rounded borders, light gray background (`#F8FAFC`), source file path headers, and indented monospace syntax.
- **Callout Alerts:** Created shaded box callouts for `NOTE`, `IMPORTANT`, `CAUTION`, and `SECURITY WARNING`.
- **Figures:** Embedded centered figures with sequential captions (`Figure X.Y — Description`), resolution scaling, and explanatory paragraphs.
