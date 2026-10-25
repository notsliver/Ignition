import React from 'react';
import { createRoot } from 'react-dom/client';
import './ui';
import { App } from './app/App';
import { AuthGate } from './app/AuthGate';

createRoot(document.getElementById('root')).render(<React.StrictMode><AuthGate><App /></AuthGate></React.StrictMode>);
