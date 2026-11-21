import os
from playwright.sync_api import sync_playwright

SCREENSHOT_DIR = os.environ.get("LOCALIZER_SCREENSHOT_DIR", "")

with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True)
    page = browser.new_page(viewport={"width": 1081, "height": 773})
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto("http://127.0.0.1:43125/", wait_until="networkidle")

    page.locator("#translated").filter(has_not_text="—").wait_for(timeout=30000)
    assert page.locator("#app-name").inner_text() == "Devin"
    assert page.locator("#status").inner_text() == "原版未汉化"
    assert page.locator("#apply").inner_text() == "应用汉化"
    assert page.locator("#open").inner_text() == "启动应用"
    assert page.locator("#restore").is_disabled()
    assert int(page.locator("#translated").inner_text().replace(",", "")) >= 31800
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
    assert page.evaluate("document.documentElement.scrollHeight <= innerHeight")

    # Verify brand logo, target emblems & rail coverage meter rendered cleanly
    logo_loaded = page.evaluate("(() => { const img = document.getElementById('brand-logo'); return Boolean(img && img.complete && img.naturalWidth > 0); })()")
    assert logo_loaded, "expected titlebar brand logo SVG to load"
    assert page.locator("#rail-icon-devin svg").count() == 1
    assert page.locator("#rail-icon-agy svg").count() == 1
    meter_box = page.locator("#rail-fill-devin").bounding_box()
    assert meter_box and meter_box["height"] >= 3 and meter_box["width"] > 60, f"expected visible rail coverage bar, got {meter_box}"

    # Verify theme single-icon toggle & continuous SVG path morphing + layered non-white shell
    theme_btn = page.locator("#theme-toggle")
    path_system = theme_btn.locator("svg path").first.get_attribute("d")
    theme_btn.hover()
    page.wait_for_timeout(240)
    path_hover = theme_btn.locator("svg path").first.get_attribute("d")
    assert path_hover != path_system, "expected hover stroke feedback on theme icon"

    theme_btn.click()
    page.mouse.move(1, 1)
    page.wait_for_timeout(240)
    assert page.evaluate("document.documentElement.getAttribute('data-theme')") == "light"
    bg_light = page.evaluate("getComputedStyle(document.body).backgroundColor")
    sheet_light = page.evaluate("getComputedStyle(document.getElementById('main')).backgroundColor")
    assert bg_light != "rgb(255, 255, 255)", f"expected layered porcelain shell, got {bg_light}"
    assert bg_light != sheet_light, "expected shell and inset studio sheet to use distinct gray layers"
    path_light = theme_btn.locator("svg path").first.get_attribute("d")
    assert path_light != path_system, "expected theme icon path morph to sun"

    if SCREENSHOT_DIR:
        page.screenshot(path=os.path.join(SCREENSHOT_DIR, "localizer-light.png"), full_page=False)

    theme_btn.click()
    page.mouse.move(1, 1)
    page.wait_for_timeout(240)
    assert page.evaluate("document.documentElement.getAttribute('data-theme')") == "dark"
    bg_dark = page.evaluate("getComputedStyle(document.body).backgroundColor")
    assert bg_dark != "rgb(0, 0, 0)", f"expected layered slate-night shell, got {bg_dark}"
    path_dark = theme_btn.locator("svg path").first.get_attribute("d")
    assert path_dark != path_light, "expected theme icon path morph to moon"

    if SCREENSHOT_DIR:
        page.screenshot(path=os.path.join(SCREENSHOT_DIR, "localizer-dark.png"), full_page=False)

    # Switch back to light for editing & layout checks
    theme_btn.click()
    theme_btn.click()
    page.wait_for_timeout(220)

    # Modal focus trap, live Mistral auto-detection, fold chevron morphing & focus restoration check
    translate_trigger = page.locator("#translate")
    translate_trigger.focus()
    translate_trigger.press("Enter")
    page.locator("#translate-dialog[open]").wait_for()
    assert page.evaluate("document.activeElement?.id") == "limit"
    page.locator("#detect-label").filter(has_not_text="正在").wait_for(timeout=5000)
    detect_text = page.locator("#detect-label").inner_text()
    assert "Mistral" in detect_text or "本地" in detect_text, f"unexpected detect label: {detect_text}"

    chev_before = page.locator("#override-chevron svg path").first.get_attribute("d")
    page.locator("#override-summary").click()
    page.wait_for_timeout(220)
    chev_after = page.locator("#override-chevron svg path").first.get_attribute("d")
    assert chev_after != chev_before, "expected fold chevron path morph on expand"

    if SCREENSHOT_DIR:
        page.screenshot(path=os.path.join(SCREENSHOT_DIR, "localizer-translate-modal.png"), full_page=False)

    page.keyboard.press("Escape")
    page.wait_for_timeout(120)
    assert page.evaluate("document.activeElement?.id") == "translate"

    # Switch to Antigravity
    page.get_by_role("button", name="Antigravity", exact=True).click()
    page.locator("#app-name").get_by_text("Antigravity", exact=True).wait_for()
    page.locator("#query").fill("New Conversation")
    page.locator("#results").get_by_text("新建会话", exact=True).first.wait_for()
    assert page.locator("#results").locator("tr").count() > 0

    # Protected kept entries & zero-missing check on Antigravity
    page.locator("#query").fill("Windows")
    page.locator("#results td.kept").first.wait_for()
    assert "Windows" in page.locator("#results td").first.inner_text()
    assert page.locator("#missing").inner_text() == "0"

    # Segmented filter pills & inline entry editing on Devin
    page.get_by_role("button", name="Devin", exact=True).click()
    page.locator("#app-name").get_by_text("Devin", exact=True).wait_for()
    page.locator("#query").fill("")
    page.locator("#filter-translated").click()
    page.locator("#results tr").first.wait_for()
    page.locator("#filter-missing").click()
    page.locator("#results tr").first.wait_for()
    page.locator("#filter-all").click()
    page.locator("#query").fill("Show {{count}} update for {{release}}")
    first_row = page.locator("#results tr").first
    first_row.get_by_text("Show {{count}} update for {{release}}", exact=True).wait_for()

    edit_btn = first_row.get_by_role("button", name="编辑译文")
    edit_btn.click()
    editor = first_row.locator("textarea.textarea-ctrl")
    editor.wait_for()
    assert first_row.locator(".token-chip").count() == 2

    # Trigger inline placeholder validation error & verify no colliding outline
    editor.fill("缺少变量的错误译文")
    editor.press("Enter")
    err_node = first_row.locator(".field-error")
    err_node.wait_for()
    assert "受保护片段" in err_node.inner_text()
    outline_style = editor.evaluate("el => getComputedStyle(el).outlineStyle")
    assert outline_style == "none", f"expected clean halo focus ring without colliding outline, got {outline_style}"

    if SCREENSHOT_DIR:
        page.screenshot(path=os.path.join(SCREENSHOT_DIR, "localizer-inline-edit.png"), full_page=False)

    # Save valid custom translation, then reset back to default
    editor.fill("展示 {{release}} 的 {{count}} 项更新")
    editor.press("Enter")
    first_row.get_by_text("展示 {{release}} 的 {{count}} 项更新", exact=True).wait_for()
    assert "自定" in first_row.locator(".state-pill").inner_text()

    reset_btn = first_row.get_by_role("button", name="恢复默认词条")
    reset_btn.click()
    first_row.get_by_text("查看 {{release}} 的 {{count}} 项更新", exact=True).wait_for()

    # Empty state & one-click filter reset
    page.locator("#query").fill("__non_existent_localizer_key_xyz__")
    page.locator("#empty").wait_for()
    page.locator("#reset-filters").click()
    page.locator("#results tr").first.wait_for()

    # Minimum window viewport overflow check
    page.set_viewport_size({"width": 761, "height": 551})
    page.wait_for_timeout(120)
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
    assert page.evaluate("document.documentElement.scrollHeight <= innerHeight")

    assert not errors, errors
    print("Wails UI: logo, layered studio theme, morphing icons, app switching, segmented filtering, inline editing + token validation, modal focus restoration, and responsive overflow passed")
    browser.close()
