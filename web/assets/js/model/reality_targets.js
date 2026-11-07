// List of popular services for VLESS Reality Target/SNI categorization and randomization
const REALITY_TARGETS_US_WEST = [
    { target: 'www.amd.com:443', sni: 'www.amd.com', region: 'us-west', name: 'AMD' },
    { target: 'www.nvidia.com:443', sni: 'www.nvidia.com', region: 'us-west', name: 'NVIDIA' },
    { target: 'www.intel.com:443', sni: 'www.intel.com', region: 'us-west', name: 'Intel' },
    { target: 'www.oracle.com:443', sni: 'www.oracle.com', region: 'us-west', name: 'Oracle' },
    { target: 'www.cisco.com:443', sni: 'www.cisco.com', region: 'us-west', name: 'Cisco' },
    { target: 'www.tesla.com:443', sni: 'www.tesla.com', region: 'us-west', name: 'Tesla' },
    { target: 'www.amazon.com:443', sni: 'www.amazon.com', region: 'us-west', name: 'Amazon' },
    { target: 'www.ebay.com:443', sni: 'www.ebay.com', region: 'us-west', name: 'eBay' },
    { target: 'www.paypal.com:443', sni: 'www.paypal.com', region: 'us-west', name: 'PayPal' },
    { target: 'www.adobe.com:443', sni: 'www.adobe.com', region: 'us-west', name: 'Adobe' },
    { target: 'addons.mozilla.org:443', sni: 'addons.mozilla.org', region: 'us-west', name: 'Mozilla Addons' },
    { target: 'www.stanford.edu:443', sni: 'www.stanford.edu', region: 'us-west', name: 'Stanford University' }
];

const REALITY_TARGETS_TOKYO = [
    { target: 'www.sony.jp:443', sni: 'www.sony.jp', region: 'tokyo', name: 'Sony JP' },
    { target: 'www.nintendo.co.jp:443', sni: 'www.nintendo.co.jp', region: 'tokyo', name: 'Nintendo JP' },
    { target: 'www.rakuten.co.jp:443', sni: 'www.rakuten.co.jp', region: 'tokyo', name: 'Rakuten JP' },
    { target: 'www.u-tokyo.ac.jp:443', sni: 'www.u-tokyo.ac.jp', region: 'tokyo', name: 'Tokyo University' },
    { target: 'www.ntt.co.jp:443', sni: 'www.ntt.co.jp', region: 'tokyo', name: 'NTT JP' }
];

const REALITY_TARGETS = [
    ...REALITY_TARGETS_US_WEST,
    ...REALITY_TARGETS_TOKYO
];

/**
 * Filter SNIs to only keep subdomains and strip apex/first-level root domains
 * @param {string|string[]} serverNames 
 * @param {string} dialHost 
 * @returns {string} Comma-separated clean SNIs
 */
function filterCleanSNIs(serverNames, dialHost) {
    const list = Array.isArray(serverNames)
        ? serverNames
        : (serverNames || '').split(',').map(s => s.trim()).filter(Boolean);
    const host = (dialHost || '').split(':')[0].trim().toLowerCase();
    const apex = host.startsWith('www.') && host.length > 4 ? host.slice(4) : '';

    const cleaned = [];
    const seen = new Set();
    for (const item of list) {
        const lower = item.toLowerCase().trim();
        if (!lower || lower.startsWith('*') || (apex && lower === apex)) {
            continue;
        }
        if (!seen.has(lower)) {
            seen.add(lower);
            cleaned.push(lower);
        }
    }
    if (host && !seen.has(host) && !host.startsWith('*')) {
        cleaned.unshift(host);
    }
    return cleaned.join(',');
}

/**
 * Returns targets for a given region
 * @param {string} region 'all' | 'us-west' | 'tokyo'
 * @returns {Array}
 */
function getTargetsByRegion(region) {
    if (region === 'us-west') return REALITY_TARGETS_US_WEST;
    if (region === 'tokyo') return REALITY_TARGETS_TOKYO;
    return REALITY_TARGETS;
}

/**
 * Returns a random Reality target configuration
 * @param {string} region Optional 'all' | 'us-west' | 'tokyo'
 * @returns {Object} Object with target and sni properties
 */
function getRandomRealityTarget(region) {
    const list = getTargetsByRegion(region);
    const randomIndex = Math.floor(Math.random() * list.length);
    const selected = list[randomIndex];
    return {
        target: selected.target,
        sni: selected.sni,
        region: selected.region
    };
}
