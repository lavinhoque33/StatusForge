import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
// Bundled Inter (variable weight, normal style); every subset is declared with
// unicode-range so browsers fetch only the subsets a page uses.
import '@fontsource-variable/inter/index.css';
// The global stylesheet must load before App: its tokens fix the layer order.
import './index.css';
import App from './App';

const rootElement = document.getElementById('root');
if (!rootElement) {
  throw new Error('StatusForge could not start: #root is missing from index.html.');
}

createRoot(rootElement).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
