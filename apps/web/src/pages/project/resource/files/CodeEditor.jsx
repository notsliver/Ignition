import { useEffect, useRef } from 'react';
import * as monaco from 'monaco-editor';
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker';
import JsonWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker';
import CssWorker from 'monaco-editor/esm/vs/language/css/css.worker?worker';
import HtmlWorker from 'monaco-editor/esm/vs/language/html/html.worker?worker';
import TsWorker from 'monaco-editor/esm/vs/language/typescript/ts.worker?worker';

/*
 * VS Code's editor (Monaco), bundled with Ignition so it works offline. Loaded only when a file is
 * opened. Each open file keeps its own model, so undo history and cursor survive switching tabs.
 */

self.MonacoEnvironment = {
  getWorker(_, label) {
    if (label === 'json') return new JsonWorker();
    if (label === 'css' || label === 'scss' || label === 'less') return new CssWorker();
    if (label === 'html' || label === 'handlebars' || label === 'razor') return new HtmlWorker();
    if (label === 'typescript' || label === 'javascript') return new TsWorker();
    return new EditorWorker();
  },
};

const special = { properties: 'ini', toml: 'ini', cfg: 'ini', conf: 'ini', env: 'shell', mcmeta: 'json', json5: 'json', snbt: 'json', lang: 'ini', log: 'plaintext', service: 'ini', dockerfile: 'dockerfile' };
export function languageFor(path) {
  const name = path.split('/').pop().toLowerCase();
  if (name === 'dockerfile' || name.startsWith('dockerfile.')) return 'dockerfile';
  if (name === 'makefile') return 'shell';
  const ext = name.includes('.') ? name.split('.').pop() : '';
  if (special[ext]) return special[ext];
  const lang = monaco.languages.getLanguages().find(l => l.extensions?.includes(`.${ext}`));
  return lang?.id ?? 'plaintext';
}

const models = new Map();
/** The model for a file, created once and kept while its tab is open. */
function modelFor(key, path, value) {
  let m = models.get(key);
  if (!m || m.isDisposed()) {
    m = monaco.editor.createModel(value, languageFor(path), monaco.Uri.parse(`file:///${encodeURIComponent(key)}/${path.split('/').pop()}`));
    models.set(key, m);
  }
  return m;
}
export function disposeModel(key) { models.get(key)?.dispose(); models.delete(key); }
export function resetModel(key, value) { const m = models.get(key); if (m && !m.isDisposed() && m.getValue() !== value) m.setValue(value); }

const themeName = () => (document.documentElement.dataset.theme === 'dark' ? 'vs-dark' : 'vs');

export default function CodeEditor({ modelKey, path, value, readOnly, onChange, onSave, onFocus }) {
  const host = useRef(null);
  const editor = useRef(null);
  const handlers = useRef({ onChange, onSave, onFocus });
  handlers.current = { onChange, onSave, onFocus };

  useEffect(() => {
    const ed = monaco.editor.create(host.current, {
      automaticLayout: true, fontSize: 12.5, lineHeight: 19, minimap: { enabled: true, scale: 1, renderCharacters: false }, scrollBeyondLastLine: false,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', renderWhitespace: 'selection', smoothScrolling: !matchMedia('(prefers-reduced-motion: reduce)').matches,
      cursorSmoothCaretAnimation: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'off' : 'on', tabSize: 2, theme: themeName(), padding: { top: 8 },
    });
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => handlers.current.onSave?.());
    ed.onDidFocusEditorText(() => handlers.current.onFocus?.());
    editor.current = ed;
    // Follow Ignition's light/dark theme.
    const obs = new MutationObserver(() => monaco.editor.setTheme(themeName()));
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    return () => { obs.disconnect(); ed.dispose(); };
  }, []);

  useEffect(() => {
    const ed = editor.current;
    const model = modelFor(modelKey, path, value ?? '');
    if (ed.getModel() !== model) ed.setModel(model);
    const sub = model.onDidChangeContent(() => handlers.current.onChange?.(model.getValue()));
    return () => sub.dispose();
  }, [modelKey]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { editor.current?.updateOptions({ readOnly: !!readOnly }); }, [readOnly]);

  return <div ref={host} className="fx-monaco" />;
}
