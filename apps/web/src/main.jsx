import React from 'react';
import { createRoot } from 'react-dom/client';
import './ui';
import { App } from './app/App';
import { AuthGate } from './app/AuthGate';
import { applyPreferences } from './app/preferences';

// Before the first paint, so sign-in screens follow the saved theme, preset and size too.
applyPreferences();

createRoot(document.getElementById('root')).render(<React.StrictMode><AuthGate><App /></AuthGate></React.StrictMode>);
