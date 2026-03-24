# LibreSpeed Portal Reskin Design

## Goal

Restyle the standalone LibreSpeed HTML page so it uses the same visual language as the Nuxt portal in `apps/web/` while preserving the existing speedtest behavior, JavaScript hooks, and DOM IDs.

## Scope

This design covers:

- a standalone static HTML document with embedded CSS
- portal-aligned typography, colors, gradients, panels, and controls
- structural HTML changes that keep existing speedtest element IDs intact
- responsive adjustments for smaller viewports

This design does not cover:

- changes to LibreSpeed test behavior
- changes to telemetry, privacy text meaning, or backend result handling
- extracting shared CSS from the Nuxt app
- converting the page into a Nuxt-rendered route

## Assumptions

- The baseline source artifact is the standalone HTML document supplied by the user on 2026-03-24, identifiable by the opening `<!DOCTYPE html>` document and the `<title>LibreSpeed Example</title>` marker.
- No matching LibreSpeed source file currently exists in this repository, so the first implementation step is to materialize that exact user-supplied HTML into a concrete workspace file before applying the reskin.
- If the user later points to a different source file, that file becomes the implementation target only if it is functionally equivalent to the approved baseline HTML.
- The page must remain self-contained, including its own font import and CSS variables, instead of depending on `apps/web/assets/css/main.css`.
- Branding must remain self-contained as well: text-only branding or inline-only decorative elements are allowed, but implementation must not add dependencies on external logo assets.

## Architecture

The speedtest page keeps its existing JavaScript logic, element IDs, canvas meters, and event flow. The implementation only changes presentation concerns:

- embedded theme tokens and font imports
- surrounding page structure and section wrappers
- meter palette constants used by the existing drawing code

The protected runtime surface is broader than IDs alone. The implementation must preserve:

- script tag order in the document head
- inline event handlers and JS-controlled nodes used by the current HTML
- existing canvas elements and their IDs
- privacy/share/start-stop/server-selection behavior
- the current `initServers()`, `startStop()`, `updateUI()`, and frame-loop execution flow

This keeps the operational behavior stable while making the page visually consistent with the Nuxt portal.

## Page Structure

The page should adopt the same portal-shell framing used by the Nuxt app:

- a dark, layered background with subtle aurora-style glow accents
- a centered shell container
- a glass-style header panel with Nexio branding and page title
- a surface/glass test area containing the server selector, start button, metrics, IP text, and share results
- a portal-styled privacy overlay panel

The current speedtest sections remain functionally the same, but they should be grouped into clearer panels:

- header and description
- server selection and primary action
- ping and jitter metrics
- download and upload gauges
- share results

## Visual Direction

The page should mirror the existing Nuxt visual system as closely as practical in a standalone file. The implementation source of truth for styling is:

- `apps/web/assets/css/main.css` for theme tokens, gradients, shadows, and glass/surface treatment
- `apps/web/components/PortalChrome.vue` for shell and header composition
- `apps/web/components/AuthCard.vue` for panel density and call-to-action treatment
- `apps/web/tailwind.config.ts` for matching color values and font families

The standalone HTML should locally reproduce the following characteristics:

- `Manrope` for headings and display text
- `Inter` for body and interface text
- dark graphite surfaces with high-contrast white text
- muted supporting text for labels and secondary content
- accent colors aligned with the portal theme:
  - primary lavender around `#bfa6ff`
  - primary dim around `#8f68ff`
  - secondary cyan around `#5adcf4`
  - tertiary pink around `#ffa0ba`

Panel styling should use rounded corners, soft borders, blur-backed glass treatment where appropriate, and shadow depth similar to the portal.

## Component Styling

### Header

The header should feel like the Nuxt `PortalChrome` component:

- rounded glass panel
- small badge or eyebrow label
- display-style title
- short supporting description
- branding handled with text and CSS treatment only, or inline-only decorative shapes if needed

### Controls

The start button should become a portal-style primary action:

- pill-shaped
- lavender gradient background in idle state
- stronger danger styling in running/abort state
- hover and focus states consistent with the portal controls

The server selector should use the same dark input treatment as the portal form fields.

### Metrics

Ping and jitter should be presented as compact stat panels that visually relate to the surrounding surfaces. Download and upload gauges should keep their canvas-based rendering but use palette colors that fit the portal accents and muted track styling.

### Privacy And Share Areas

The privacy overlay should no longer switch to a white document-style block. It should read as a modal surface over the dark page, with readable text contrast and consistent link/button treatment. The share results area should appear as a secondary portal panel rather than plain stacked elements.

## Responsive Behavior

The page must remain readable and operable at narrow widths:

- panels stack vertically on smaller screens
- gauge areas preserve enough height for readable meter text
- selector, button, and share URL remain easy to tap
- privacy overlay remains scrollable within the viewport

Acceptance should be checked at a minimum viewport width of `360px`. Interactive controls should maintain an effective tap target close to `44px` in height wherever practical, especially for the primary action, close/privacy controls, and server selector.

The implementation should favor CSS layout changes over JavaScript changes.

## Risks And Constraints

- LibreSpeed depends on specific element IDs and existing meter canvases, so the redesign must not rename or remove those hooks.
- The meter layout depends on fixed sizing assumptions; large structural changes should avoid breaking the canvas rendering area.
- Because the file is standalone, all theme tokens and font imports must be duplicated locally rather than shared from the Nuxt app.
- Because no source file currently exists in the repo, implementation must first create or receive a concrete HTML file path for the approved baseline artifact before style changes can be applied and verified.

## Acceptance Criteria

- the page remains a standalone HTML file
- the implementation starts from the approved 2026-03-24 baseline HTML artifact or a functionally equivalent file explicitly provided by the user
- all existing speedtest IDs and behaviors continue to work
- script tag order, inline handlers, and JS-controlled sections continue to function
- the page uses `Manrope` and `Inter`
- the page adopts the Nuxt portal dark palette and glass/surface treatment based on `apps/web/assets/css/main.css`, `apps/web/components/PortalChrome.vue`, `apps/web/components/AuthCard.vue`, and `apps/web/tailwind.config.ts`
- the start button, selector, privacy overlay, and share section all match the portal aesthetic
- the download/upload meter colors are updated to fit the new theme
- the layout remains usable at `360px` viewport width without clipped controls or inaccessible overlay content
