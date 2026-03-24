# LibreSpeed Portal Reskin Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a standalone LibreSpeed HTML page at `apps/web/public/librespeed/index.html` that preserves the approved baseline behavior while adopting the Nuxt portal’s fonts, color palette, and glass-panel styling.

**Architecture:** Materialize the approved baseline HTML as a standalone static document under the Nuxt app’s `public/` tree so it can be served without becoming a Nuxt-rendered route. Keep the original script behavior and DOM hooks intact, then replace the surrounding document structure, CSS variables, and visual palette with a self-contained portal-shell reskin based on the existing Nuxt theme sources.

**Tech Stack:** Static HTML, inline CSS, existing LibreSpeed JavaScript, Nuxt `public/` asset serving, Google Fonts import

---

### Task 1: Materialize The Approved Baseline HTML

**Files:**
- Create: `apps/web/public/librespeed/index.html`
- Reference: `docs/superpowers/specs/2026-03-24-librespeed-portal-reskin-design.md`

- [ ] **Step 1: Write the failing test**

Confirm the target file does not exist yet.

Run: `test -f apps/web/public/librespeed/index.html; echo $?`
Expected: `1`

- [ ] **Step 2: Run test to verify it fails**

Check that the approved LibreSpeed baseline markers are not yet present in the workspace.

Run: `rg -n "LibreSpeed Example|speedtest.js|servers_list.js" apps/web/public`
Expected: no matches

- [ ] **Step 3: Write minimal implementation**

Create `apps/web/public/librespeed/index.html` from the approved 2026-03-24 baseline HTML supplied by the user. Preserve the original:

- document script tags and their order
- element IDs
- inline event handlers
- speedtest JavaScript logic and meter canvases
- privacy/share/start-stop/server-selection markup hooks

- [ ] **Step 4: Run test to verify it passes**

Run:
- `test -f apps/web/public/librespeed/index.html; echo $?`
- `rg -n "LibreSpeed Example|speedtest.js|servers_list.js|id=\"startStopBtn\"|id=\"dlMeter\"|id=\"ulMeter\"" apps/web/public/librespeed/index.html`

Expected:
- first command prints `0`
- second command finds the expected baseline markers

- [ ] **Step 5: Commit**

```bash
git add apps/web/public/librespeed/index.html
git commit -m "feat: add baseline librespeed page"
```

### Task 2: Apply Portal Theme Tokens And Background Treatment

**Files:**
- Modify: `apps/web/public/librespeed/index.html`
- Reference: `apps/web/assets/css/main.css`
- Reference: `apps/web/tailwind.config.ts`

- [ ] **Step 1: Write the failing test**

Confirm the baseline file still uses the old visual defaults.

Run: `rg -n 'font-family:"Roboto"|background:#FFFFFF|color:#202020|#6060AA|#616161' apps/web/public/librespeed/index.html`
Expected: matches for the legacy font, light background, and old meter/button colors

- [ ] **Step 2: Run test to verify it fails**

Check that the standalone page does not yet contain the portal font import or portal token names.

Run: `rg -n "Manrope|Inter|--surface|--primary|portal-page|portal-shell" apps/web/public/librespeed/index.html`
Expected: no matches

- [ ] **Step 3: Write minimal implementation**

Replace the legacy `<style>` block and related color constants so the page becomes self-contained but portal-aligned:

- import `Manrope` and `Inter` with a Google Fonts CSS import
- add local CSS variables matching the portal palette from `apps/web/assets/css/main.css` and `apps/web/tailwind.config.ts`
- replace the plain white page background with the layered dark gradient and aurora accents
- update the JavaScript meter color constants to use portal-friendly values for meter tracks, download, upload, and progress

- [ ] **Step 4: Run test to verify it passes**

Run:
- `rg -n "Manrope|Inter|--surface:|--primary:|radial-gradient|linear-gradient\\(180deg, #040405" apps/web/public/librespeed/index.html`
- `rg -n 'const dlColor = "#|const meterBk = ' apps/web/public/librespeed/index.html`

Expected:
- first command finds the embedded font import, theme tokens, and dark gradient background
- second command shows the updated portal-aligned meter palette constants

- [ ] **Step 5: Commit**

```bash
git add apps/web/public/librespeed/index.html
git commit -m "style: add portal theme tokens to librespeed page"
```

### Task 3: Restructure The HTML Into Portal Shell Panels

**Files:**
- Modify: `apps/web/public/librespeed/index.html`
- Reference: `apps/web/components/PortalChrome.vue`
- Reference: `apps/web/components/AuthCard.vue`

- [ ] **Step 1: Write the failing test**

Confirm the page still uses the original flat structure rather than portal shell wrappers.

Run: `rg -n 'id="loading"|id="testWrapper"|<h1>LibreSpeed Example</h1>|div class="testArea"|div class="testArea2"' apps/web/public/librespeed/index.html`
Expected: matches for the original structure only

- [ ] **Step 2: Run test to verify it fails**

Check that portal composition classes are not yet present.

Run: `rg -n "portal-page|portal-aurora|portal-shell|glass|surface|badge|section-title" apps/web/public/librespeed/index.html`
Expected: no matches or only partial matches introduced in Task 2 CSS without structural usage

- [ ] **Step 3: Write minimal implementation**

Refactor the HTML body structure while preserving behavior:

- wrap the page in `portal-page`, `portal-aurora`, and `portal-shell` containers
- replace the plain `<h1>` area with a glass-style branded header inspired by `PortalChrome.vue`
- move the server selector and start button into styled control panels
- group ping/jitter and download/upload into surface or glass cards
- restyle the share area and privacy overlay into portal-style panels
- keep all existing IDs, canvases, and inline handlers intact

- [ ] **Step 4: Run test to verify it passes**

Run:
- `rg -n "portal-page|portal-aurora|portal-shell|glass|surface|badge|section-title" apps/web/public/librespeed/index.html`
- `rg -n 'id="loading"|id="testWrapper"|id="startStopBtn"|id="server"|id="pingText"|id="jitText"|id="dlMeter"|id="ulMeter"|id="shareArea"|id="privacyPolicy"' apps/web/public/librespeed/index.html`

Expected:
- first command finds the new portal wrapper structure
- second command confirms all required runtime hooks still exist

- [ ] **Step 5: Commit**

```bash
git add apps/web/public/librespeed/index.html
git commit -m "style: restructure librespeed page into portal shell"
```

### Task 4: Finish Responsive States And Interaction Styling

**Files:**
- Modify: `apps/web/public/librespeed/index.html`

- [ ] **Step 1: Write the failing test**

Inspect the responsive and stateful CSS sections before final cleanup.

Run: `rg -n "@media|#startStopBtn.running|#privacyPolicy|#shareArea|select|input" apps/web/public/librespeed/index.html`
Expected: matches reveal legacy responsive rules or incomplete portal-state styling

- [ ] **Step 2: Run test to verify it fails**

Check that the file does not yet encode the explicit mobile target from the spec.

Run: `rg -n "360px|44px|min-height: 44px|min-height:44px" apps/web/public/librespeed/index.html`
Expected: no matches

- [ ] **Step 3: Write minimal implementation**

Finalize the reskin for usability and state handling:

- add responsive layout rules that remain usable at `360px` viewport width
- size the primary action, selector, and privacy controls to approximately `44px` height
- ensure loading, hidden/visible, share panel, and privacy overlay states remain legible on dark surfaces
- polish focus, hover, and running/abort styles so the page feels consistent with the portal controls

- [ ] **Step 4: Run test to verify it passes**

Run:
- `rg -n "360px|44px|min-height" apps/web/public/librespeed/index.html`
- `rg -n "#startStopBtn.running|#privacyPolicy|#shareArea|@media all and \\(max-width:40em\\)|@media" apps/web/public/librespeed/index.html`

Expected:
- first command finds explicit responsive/tap-target sizing guards
- second command finds the updated state and responsive rules

- [ ] **Step 5: Commit**

```bash
git add apps/web/public/librespeed/index.html
git commit -m "style: finish responsive librespeed portal states"
```

### Task 5: Verify Behavior Preservation End To End

**Files:**
- Test: `apps/web/public/librespeed/index.html`
- Reference: `docs/superpowers/specs/2026-03-24-librespeed-portal-reskin-design.md`

- [ ] **Step 1: Write the failing test**

Run a final structural verification before signoff.

Run: `rg -n 'speedtest.js|servers_list.js|onload="initServers\\(\\)"|onclick="startStop\\(\\)"|onclick="I\\('\''privacyPolicy'\''\\)\\.style\\.display=' apps/web/public/librespeed/index.html`
Expected: confirm all critical runtime hooks are present; if any are missing, treat that as a failure to fix before completion

- [ ] **Step 2: Run test to verify it fails**

Manually inspect for accidental regressions in script order, missing IDs, or removed inline handlers.

Run: `sed -n '1,260p' apps/web/public/librespeed/index.html`
Expected: the document still includes the original script flow and runtime hooks with only presentation changes around them

- [ ] **Step 3: Write minimal implementation**

Fix any remaining regressions found in the final verification pass. Do not add new behavior beyond what is required to preserve the baseline speedtest page and apply the approved portal-shell reskin.

- [ ] **Step 4: Run test to verify it passes**

Run:
- `rg -n 'id="message"|id="loading"|id="testWrapper"|id="startStopBtn"|id="server"|id="pingText"|id="jitText"|id="dlText"|id="ulText"|id="shareArea"|id="privacyPolicy"|id="resultsURL"|id="resultsImg"' apps/web/public/librespeed/index.html`
- `rg -n 'speedtest.js|servers_list.js|onload="initServers\\(\\)"|onclick="startStop\\(\\)"|window.requestAnimationFrame' apps/web/public/librespeed/index.html`

Expected:
- first command finds every required DOM hook
- second command confirms the baseline runtime wiring remains intact

- [ ] **Step 5: Commit**

```bash
git add apps/web/public/librespeed/index.html
git commit -m "test: verify librespeed portal reskin behavior"
```
