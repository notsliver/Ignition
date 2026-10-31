// Interface size (Settings › Preferences) zooms the page. Rects are measured in zoomed pixels,
// but a fixed-position popover's top/left are zoomed again, so positions are divided back.
export const pageZoom = () => parseFloat(document.documentElement.style.zoom) || 1;
