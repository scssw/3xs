export type RealityRegionCategory = 'all' | 'us-west' | 'tokyo';

export interface RealityTargetItem {
  target: string;
  host: string;
  region: 'us-west' | 'tokyo';
  name: string;
}

export const US_WEST_TARGETS: RealityTargetItem[] = [
  { target: 'www.amd.com:443', host: 'www.amd.com', region: 'us-west', name: 'AMD' },
  { target: 'www.nvidia.com:443', host: 'www.nvidia.com', region: 'us-west', name: 'NVIDIA' },
  { target: 'www.intel.com:443', host: 'www.intel.com', region: 'us-west', name: 'Intel' },
  { target: 'www.oracle.com:443', host: 'www.oracle.com', region: 'us-west', name: 'Oracle' },
  { target: 'www.cisco.com:443', host: 'www.cisco.com', region: 'us-west', name: 'Cisco' },
  { target: 'www.tesla.com:443', host: 'www.tesla.com', region: 'us-west', name: 'Tesla' },
  { target: 'www.amazon.com:443', host: 'www.amazon.com', region: 'us-west', name: 'Amazon' },
  { target: 'www.ebay.com:443', host: 'www.ebay.com', region: 'us-west', name: 'eBay' },
  { target: 'www.paypal.com:443', host: 'www.paypal.com', region: 'us-west', name: 'PayPal' },
  { target: 'www.adobe.com:443', host: 'www.adobe.com', region: 'us-west', name: 'Adobe' },
  {
    target: 'addons.mozilla.org:443',
    host: 'addons.mozilla.org',
    region: 'us-west',
    name: 'Mozilla Addons',
  },
  {
    target: 'www.stanford.edu:443',
    host: 'www.stanford.edu',
    region: 'us-west',
    name: 'Stanford University',
  },
];

export const TOKYO_TARGETS: RealityTargetItem[] = [
  { target: 'www.sony.jp:443', host: 'www.sony.jp', region: 'tokyo', name: 'Sony Japan' },
  {
    target: 'www.nintendo.co.jp:443',
    host: 'www.nintendo.co.jp',
    region: 'tokyo',
    name: 'Nintendo Japan',
  },
  {
    target: 'www.rakuten.co.jp:443',
    host: 'www.rakuten.co.jp',
    region: 'tokyo',
    name: 'Rakuten Japan',
  },
  {
    target: 'www.u-tokyo.ac.jp:443',
    host: 'www.u-tokyo.ac.jp',
    region: 'tokyo',
    name: 'University of Tokyo',
  },
  { target: 'www.ntt.co.jp:443', host: 'www.ntt.co.jp', region: 'tokyo', name: 'NTT Japan' },
];

export const ALL_TARGETS: RealityTargetItem[] = [...US_WEST_TARGETS, ...TOKYO_TARGETS];

export function getTargetsByRegion(region: RealityRegionCategory): RealityTargetItem[] {
  if (region === 'us-west') return US_WEST_TARGETS;
  if (region === 'tokyo') return TOKYO_TARGETS;
  return ALL_TARGETS;
}

export function getTargetsCSVByRegion(region: RealityRegionCategory): string {
  return getTargetsByRegion(region)
    .map((item) => item.target)
    .join(',');
}

export function findTargetRegion(targetOrHost: string): 'us-west' | 'tokyo' | null {
  const clean = targetOrHost.replace(/:\d+$/, '').trim().toLowerCase();
  const match = ALL_TARGETS.find(
    (item) =>
      item.host.toLowerCase() === clean || item.target.toLowerCase().startsWith(clean + ':'),
  );
  return match ? match.region : null;
}

// Strip parent/apex domains so only specific subdomains (like www.amd.com) are kept in SNI.
export function filterCleanSNIs(serverNames: string[] | undefined, targetHost: string): string[] {
  const cleanHost = targetHost.replace(/:\d+$/, '').trim().toLowerCase();
  if (!serverNames || serverNames.length === 0) {
    return cleanHost ? [cleanHost] : [];
  }
  const filtered = serverNames
    .map((s) => s.trim())
    .filter((s) => {
      const lower = s.toLowerCase();
      if (!lower) return false;
      // Drop apex domain if targetHost is a subdomain (e.g. drop amd.com for www.amd.com).
      if (cleanHost && cleanHost !== lower && cleanHost.endsWith('.' + lower)) {
        return false;
      }
      return true;
    });
  if (cleanHost && !filtered.includes(cleanHost)) {
    return [cleanHost, ...filtered];
  }
  return filtered.length > 0 ? filtered : [cleanHost];
}
