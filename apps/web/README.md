# Ignition Web

The Ignition web app, built with React 19, Vite and Heroicons.

## Scripts

| Command | Description |
| --- | --- |
| `npm install` | Install dependencies |
| `npm run dev` | Start the dev server at http://127.0.0.1:5173 |
| `npm run build` | Build for production into `dist/` |
| `npm run preview` | Serve the production build locally |

## Structure

```
src/
├─ main.jsx              Entry point: loads UI styles and renders <App />
├─ ui/                   UI library: generic, app-agnostic building blocks
│  ├─ index.js           Public entry: import { Badge, Sidebar } from '../ui'
│  ├─ styles/
│  │  ├─ tokens.css      Design tokens for light and dark themes
│  │  └─ base.css        Reset, focus ring, reduced-motion rules
│  ├─ utils/cx.js        Class-name helper
│  └─ components/        One folder per component (Name.jsx + Name.css)
│     ├─ Badge/
│     ├─ IconButton/
│     ├─ SegmentedControl/
│     └─ Sidebar/        Sidebar, SidebarHeader, SidebarNav, SidebarItem,
│                        SidebarGroup, SidebarSubItem, SidebarFooter
├─ app/                  Ignition-specific shell
│  ├─ App.jsx / App.css  Layout: sidebar + main container, hide and mobile states
│  ├─ AppSidebar.jsx     Sidebar composed from UI primitives
│  └─ navigation.js      Sidebar menu definition
├─ hooks/
│  └─ useTheme.js        Light/dark theme, persisted to localStorage
└─ pages/                Page content (currently ComingSoon)
```

### Where code goes

- **`ui/`** holds anything reusable that doesn't know about Ignition. Components may import only from inside `ui/`.
- **`app/`** holds the app shell, composition and configuration.
- **`pages/`** holds what renders inside the main container.
- **`hooks/`** holds shared React hooks used by the app.

## Theming

Colours are CSS custom properties defined in [`src/ui/styles/tokens.css`](src/ui/styles/tokens.css). Dark mode overrides them under `:root[data-theme="dark"]`, and `useTheme` sets that attribute. Use tokens (`var(--surface)`, `var(--text)`, …) rather than hard-coded colours, so new components support both themes automatically.

## Adding a component

1. Create `src/ui/components/Name/Name.jsx` and `Name.css`.
2. Import the CSS from the component file.
3. Export it from `src/ui/index.js`.

## Adding a sidebar item

Edit [`src/app/navigation.js`](src/app/navigation.js). Top-level items take a Heroicons icon (`@heroicons/react/20/solid`); group items are plain labels.

## Conventions

- Use Heroicons only; don't use Lucide.
- Keep the UI compact and restrained.
- Every animation needs a `prefers-reduced-motion` fallback. `base.css` disables transitions globally.
