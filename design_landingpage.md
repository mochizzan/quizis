# Livo AI Landing Page - Design System & Layout Analysis

This document provides a comprehensive structural, visual, and behavioral analysis of the Livo AI landing page hero section shown in the screenshot (`image_f95185.png`). The specification is detailed enough for a developer or LLM to reconstruct the layout and visual styling accurately.

---

## 1. Global Layout & Structure

The landing page features a centered, wide-container layout with generous padding and a warm, low-contrast background.

- **Background Color:** Warm off-white / light cream (`#FAF6F2` or similar).
- **Grid Structure:** Single-column layout divided into major vertical sections:
  1. **Header (Navigation & Branding)**
  2. **Hero Section (Two-Column Split)**
     - **Left Column:** Value proposition text, dual CTAs, and trial microcopy.
     - **Right Column:** Complex UI mockup collage (central audio/meeting analysis card surrounded by participant video feeds and floating widgets connected via subtle vector lines).
  3. **Promotional Banner:** Full-width centered banner highlighting a discount offer.
  4. **Social Proof / Client Logos Footer:** Horizontal alignment of brand logos.

---

## 2. Style & Visual Design

### 2.1 Color Palette
- **Background:** `#FAF6F2` (Warm cream)
- **Primary Text:** `#111827` (Dark charcoal / near black)
- **Secondary Text:** `#4B5563` (Muted gray)
- **Accent Gradient:** Linear gradient from Purple (`#8B5CF6`) to Orange (`#F97316`) used for headings and primary CTA buttons.
- **Card Backgrounds:** Pure white (`#FFFFFF`) with subtle soft shadows and thin light borders (`#E5E7EB`).

### 2.2 Typography
- **Font Family:** Modern Sans-Serif (e.g., Inter, system-ui).
- **H1 (Main Hero Heading):** Extra bold, large display size (`~48px - 56px`), tight line height. The first two words ("AI analysis") feature the primary purple-to-orange text gradient, while the remaining text is solid black.
- **Body Copy:** Regular weight (`~16px`), relaxed line height (`1.5`), muted gray color.
- **Buttons & Badges:** Medium/Semi-bold weight (`~14px`).

### 2.3 UI Styling & Effects
- **Border Radius:** 
  - Buttons: Fully rounded or pill-shaped (`9999px` or `28px`).
  - Cards & Video Tiles: Rounded rectangles with medium-to-large corner radius (`16px` - `24px`).
- **Shadows:** Soft, diffused drop shadows on floating cards (`box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.05), 0 8px 10px -6px rgba(0, 0, 0, 0.05)`).
- **Borders:** Thin 1px solid borders with low opacity for separation and depth.

---

## 3. Component Breakdown

### 3.1 Header / Navigation
- **Logo (Left):** Black rounded-square icon container with a white stylized geometric mark inside, paired with clean spacing.
- **Navigation Links (Center):** Horizontal list (`How it works`, `Use cases`, `Features`, `Pricing`, `FAQ`) in dark text with hover state implied.
- **Action Icons (Right):** Two circular icon buttons (History/Refresh icon and User Profile avatar icon).

### 3.2 Hero Left Content
- **Heading:** "AI analysis for real-time discussions" (Gradient on "AI analysis").
- **Paragraph:** "Livo AI records your meetings, recognizes who's speaking, and provides real-time insights and live recommendations — all without taking manual notes."
- **CTA Buttons:**
  - **Primary Button:** Gradient background (Purple to Orange), white text ("Start for free"), containing a right-pointing arrow inside a circle icon.
  - **Secondary Button:** White background, dark border, dark text ("Contact us").
- **Microcopy:** Bullet-separated feature highlights below buttons: `• 31-day free trial  • No credit card required  • Cancel anytime`.

### 3.3 Hero Right Visuals (UI Mockup Collage)
- **Central Device / Meeting Card:**
  - Header: "End-of-Sprint Meeting" with a three-dot menu icon.
  - Audio Waveform: Dynamic audio wave visualization bars.
  - Status Indicator: Timestamp "00:05:39" and badge "Analysing...".
  - Live Transcript / Chat Cards: User avatar (Conrad), badge ("HB 16:45"), and text snippet ("The only thing left is to get the final illustration, and..."). Additional participant chat lines below.
  - Play/Pause Control: Circular black pause button at the bottom center of the card.
- **Floating Rating Widget:** Top-right floating badge reading "5.0/5.0 #1 Rated + 6,000+ Reviews" with star icons.
- **Participant Video Tiles:** Four rounded rectangular cards showing video feeds of individuals in different settings (office, home, bookshelf). Each tile features a miniature audio/video status toggle icon at the bottom.
- **Connection Lines:** A thin curved orange/purple line with a glowing node (`✦`) connecting a participant tile to the central dashboard, implying real-time linkage.

### 3.4 Promotional Banner
- Centered text banner: "Enjoy 50% off premium features for first 3 months — 21 days remaining" followed by an inline action link "Start 14 days trial" with an arrow icon.

### 3.5 Client Logos Footer
- Single row displaying partner/client company wordmarks with distinct logo marks:
  1. **SHELLS** (Concentric circles icon)
  2. **SmartFinder** (Geometric polygon icon)
  3. **Zoomerr** (Lightning circle icon)
  4. **kontrastr** (Split geometric icon)
  5. **WAVESMARATHON** (Vertical wave lines icon)

---

## 4. UI/UX & Interaction Assumptions

- **Hover States:** Navigation links and secondary buttons likely darken or show subtle background fills on hover. Primary gradient button likely features a brightness shift or shadow expansion.
- **Interactivity:** Clicking "Start for free" or trial links triggers an onboarding modal or redirects to a signup flow.
- **Responsive Behavior (Assumed):** On smaller viewports (mobile/tablet), the two-column hero stack vertically, with the video collage scaling down or moving below the text.