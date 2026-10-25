import React from 'react';
import { createRoot } from 'react-dom/client';
import './ui';
import { App } from './app/App';
import { AuthGate } from './app/AuthGate';
import { initialTheme } from './hooks/useTheme';

// Before the first paint, so sign-in screens follow the saved theme too.
document.documentElement.dataset.theme = initialTheme();

createRoot(document.getElementById('root')).render(<React.StrictMode><AuthGate><App /></AuthGate></React.StrictMode>);
