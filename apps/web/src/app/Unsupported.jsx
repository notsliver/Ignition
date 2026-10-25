import { InformationCircleIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, Notice } from '../ui';

/** Explains, honestly, that an action isn't supported by this version yet. */
export function Unsupported({ info, onClose }) {
  return <Dialog open={!!info} onClose={onClose} title={info?.title} icon={InformationCircleIcon} description={info?.body} footer={<Button onClick={onClose}>Got it</Button>} />;
}

/** Banner for pages that still show sample data instead of live data from the API. */
export function PreviewBanner({ children }) {
  return <div className="preview-banner"><Notice tone="info" title="Preview with sample data">{children ?? 'This page isn\'t connected to the API yet. Nothing here reflects or changes your real resources.'}</Notice></div>;
}
