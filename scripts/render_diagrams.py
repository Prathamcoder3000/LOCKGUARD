import os
import time
from selenium import webdriver
from selenium.webdriver.chrome.options import Options

def render_diagrams():
    input_dir = os.path.abspath('docs/diagrams')
    output_dir = os.path.abspath('cookbook-evidence')
    os.makedirs(output_dir, exist_ok=True)

    diagram_files = [f for f in os.listdir(input_dir) if f.endswith('.mmd')]
    print(f"Found {len(diagram_files)} diagrams to render: {diagram_files}")

    options = Options()
    options.add_argument('--headless')
    options.add_argument('--disable-gpu')
    options.add_argument('--window-size=1920,1400')
    driver = webdriver.Chrome(options=options)

    html_template = """<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
    <script>
        mermaid.initialize({
            startOnLoad: true,
            theme: 'default',
            themeVariables: {
                primaryColor: '#E2E8F0',
                primaryTextColor: '#0F172A',
                primaryBorderColor: '#3B82F6',
                lineColor: '#2563EB',
                secondaryColor: '#EFF6FF',
                tertiaryColor: '#F8FAFC'
            },
            fontFamily: 'Inter, Segoe UI, sans-serif'
        });
    </script>
    <style>
        body {
            background-color: #FFFFFF;
            margin: 20px;
            display: inline-block;
        }
        #diagram-container {
            padding: 20px;
            background: #FFFFFF;
            border-radius: 8px;
        }
    </style>
</head>
<body>
    <div id="diagram-container">
        <div class="mermaid">
__MERMAID_CODE__
        </div>
    </div>
</body>
</html>"""

    temp_html_path = os.path.join(output_dir, 'temp_render.html')

    for fname in sorted(diagram_files):
        src_path = os.path.join(input_dir, fname)
        with open(src_path, 'r', encoding='utf-8') as f:
            code = f.read()

        page_html = html_template.replace('__MERMAID_CODE__', code)
        with open(temp_html_path, 'w', encoding='utf-8') as f:
            f.write(page_html)

        driver.get('file:///' + temp_html_path.replace('\\', '/'))
        time.sleep(2.5)  # Allow mermaid SVG rendering to complete

        try:
            container = driver.find_element('id', 'diagram-container')
            base_name = os.path.splitext(fname)[0]
            out_png = os.path.join(output_dir, f"diag_{base_name}.png")
            container.screenshot(out_png)
            print(f"Rendered: {fname} -> {out_png} ({os.path.getsize(out_png)} bytes)")
        except Exception as e:
            print(f"Failed to render {fname}: {e}")

    driver.quit()
    if os.path.exists(temp_html_path):
        os.remove(temp_html_path)
    print("All diagrams rendered successfully!")

if __name__ == '__main__':
    render_diagrams()
