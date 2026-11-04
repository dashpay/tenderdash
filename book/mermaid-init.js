// Mermaid diagrams share the book's light or dark theme.
document.querySelectorAll('code.language-mermaid').forEach((code) => {
    const diagram = document.createElement('pre');
    diagram.className = 'mermaid';
    diagram.textContent = code.textContent;
    code.parentElement.replaceWith(diagram);
});
const isDark = () => ['ayu', 'navy', 'coal'].some((theme) => document.documentElement.classList.contains(theme));
const initialDark = isDark();
mermaid.initialize({ startOnLoad: true, theme: initialDark ? 'dark' : 'default', securityLevel: 'strict' });
// Reload diagrams when switching between light and dark themes, as in Platform.
new MutationObserver(() => {
    if (isDark() !== initialDark) {
        window.location.reload();
    }
}).observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
