import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Input, Modal, Segmented, Space, Table, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import type { RealityScanResult } from '@/generated/types';
import {
  findTargetRegion,
  getTargetsCSVByRegion,
  type RealityRegionCategory,
} from '@/lib/xray/reality-targets';

// xray-core ML-DSA-65 REALITY min peer cert-chain size (not defined in this repo).
export const MLDSA65_MIN_CERT_CHAIN_BYTES = 3500;

interface RealityTargetScannerModalProps {
  open: boolean;
  onClose: () => void;
  scanRealityCandidates: (targets?: string) => Promise<RealityScanResult[]>;
  onPick: (result: RealityScanResult) => void;
  mldsa65Enabled?: boolean;
}

export default function RealityTargetScannerModal({
  open,
  onClose,
  scanRealityCandidates,
  onPick,
  mldsa65Enabled = false,
}: RealityTargetScannerModalProps) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [query, setQuery] = useState('');
  const [region, setRegion] = useState<RealityRegionCategory>('all');
  const [results, setResults] = useState<RealityScanResult[]>([]);
  const scanRef = useRef(scanRealityCandidates);
  useEffect(() => {
    scanRef.current = scanRealityCandidates;
  });

  const applyScan = useCallback(async (targets?: string) => {
    try {
      setResults(await scanRef.current(targets));
    } finally {
      setLoading(false);
    }
  }, []);

  const runScan = useCallback(
    (targets?: string) => {
      setLoading(true);
      setResults([]);
      void applyScan(targets);
    },
    [applyScan],
  );

  const handleRegionChange = (val: RealityRegionCategory) => {
    setRegion(val);
    setQuery('');
    runScan(getTargetsCSVByRegion(val));
  };

  const pickLowestLatency = () => {
    const candidates = results.filter(
      (r) => r.feasible && r.h2 && r.tls13 && r.latencyMs > 0 && !r.privateTarget,
    );
    if (candidates.length > 0) {
      candidates.sort((a, b) => a.latencyMs - b.latencyMs);
      onPick(candidates[0]);
      onClose();
      return;
    }
    const anyFeasible = results.filter((r) => r.feasible && !r.privateTarget);
    if (anyFeasible.length > 0) {
      anyFeasible.sort((a, b) => (a.latencyMs || 9999) - (b.latencyMs || 9999));
      onPick(anyFeasible[0]);
      onClose();
    }
  };

  // Clearing the previous results is done during render so the auto-scan effect
  // carries only the request itself.
  const [scannedOpen, setScannedOpen] = useState(false);
  if (open !== scannedOpen) {
    setScannedOpen(open);
    if (open) {
      setResults([]);
      setLoading(true);
    }
  }

  useEffect(() => {
    if (open) void applyScan();
  }, [open, applyScan]);

  const columns: ColumnsType<RealityScanResult> = [
    {
      title: t('pages.inbounds.form.realityRegion'),
      key: 'region',
      width: 90,
      render: (_, row) => {
        const reg = findTargetRegion(row.target || row.host);
        if (reg === 'us-west') {
          return <Tag color="blue">{t('pages.inbounds.form.realityCategoryUSWest')}</Tag>;
        }
        if (reg === 'tokyo') {
          return <Tag color="purple">{t('pages.inbounds.form.realityCategoryTokyo')}</Tag>;
        }
        return <Tag>{t('pages.inbounds.form.realityCategoryAll')}</Tag>;
      },
    },
    {
      title: t('pages.inbounds.form.target'),
      dataIndex: 'target',
      key: 'target',
      width: 190,
      render: (target: string, row) => (
        <Tooltip title={row.ip ? `${target} — ${row.ip}` : target}>
          <div style={{ lineHeight: 1.25 }}>
            <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {target}
            </div>
            {row.ip ? <div style={{ color: '#999', fontSize: 12 }}>{row.ip}</div> : null}
          </div>
        </Tooltip>
      ),
    },
    {
      title: t('pages.inbounds.form.scanStatus'),
      dataIndex: 'feasible',
      key: 'feasible',
      width: 90,
      render: (feasible: boolean, row) =>
        feasible ? (
          <Tag color="success">{t('pages.inbounds.form.scanFeasible')}</Tag>
        ) : (
          <Tooltip title={row.reason}>
            <Tag color="warning">{t('pages.inbounds.form.scanNotFeasible')}</Tag>
          </Tooltip>
        ),
    },
    {
      title: 'TLS',
      dataIndex: 'tlsVersion',
      key: 'tlsVersion',
      width: 60,
      render: (v: string) => v || '—',
    },
    {
      title: 'ALPN / H2',
      dataIndex: 'alpn',
      key: 'alpn',
      width: 100,
      render: (v: string, row) =>
        row.h2 ? <Tag color="cyan">H2 ({v || 'h2'})</Tag> : <Tag color="default">{v || '—'}</Tag>,
    },
    {
      title: t('pages.inbounds.form.scanCurve'),
      dataIndex: 'curveID',
      key: 'curveID',
      width: 130,
      render: (v: string) => v || '—',
    },
    {
      title: t('pages.inbounds.form.scanCert'),
      dataIndex: 'certSubject',
      key: 'certSubject',
      width: 160,
      ellipsis: true,
      render: (_: string, row) =>
        row.certValid ? (
          <Tooltip title={`${row.certSubject} (${row.certIssuer})`}>
            <span>{row.certSubject || '—'}</span>
          </Tooltip>
        ) : (
          <Tag>{t('pages.inbounds.form.scanCertInvalid')}</Tag>
        ),
    },
    {
      title: t('pages.inbounds.form.scanCertChain'),
      dataIndex: 'certChainBytes',
      key: 'certChainBytes',
      width: 100,
      render: (bytes: number) => {
        if (!bytes) return '—';
        if (mldsa65Enabled && bytes < MLDSA65_MIN_CERT_CHAIN_BYTES) {
          return (
            <Tooltip
              title={t('pages.inbounds.form.scanMldsaCertChainTooSmall', {
                length: bytes,
                min: MLDSA65_MIN_CERT_CHAIN_BYTES,
              })}
            >
              <Tag color="warning">{bytes} B</Tag>
            </Tooltip>
          );
        }
        return `${bytes} B`;
      },
    },
    {
      title: t('pages.inbounds.form.scanLatency'),
      dataIndex: 'latencyMs',
      key: 'latencyMs',
      width: 85,
      render: (v: number) => (v > 0 ? `${v} ms` : '—'),
    },
    {
      title: '',
      key: 'action',
      width: 64,
      render: (_, row) => (
        <Button
          type="link"
          size="small"
          onClick={() => {
            onPick(row);
            onClose();
          }}
        >
          {t('pages.inbounds.form.scanUse')}
        </Button>
      ),
    },
  ];

  return (
    <Modal
      open={open}
      onCancel={onClose}
      footer={[
        <Button
          key="autopick"
          type="primary"
          onClick={pickLowestLatency}
          disabled={!results.some((r) => r.feasible)}
        >
          {t('pages.inbounds.form.realityAutoSelect')}
        </Button>,
        <Button
          key="rescan"
          onClick={() => runScan(query.trim() || getTargetsCSVByRegion(region))}
          loading={loading}
        >
          {t('pages.inbounds.form.scanRescan')}
        </Button>,
        <Button key="close" onClick={onClose}>
          {t('close')}
        </Button>,
      ]}
      title={t('pages.inbounds.form.scanModalTitle')}
      width={1080}
    >
      <Space orientation="vertical" size="small" style={{ width: '100%' }}>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
          {t('pages.inbounds.form.scanModalDesc')}
        </Typography.Paragraph>
        <div style={{ display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap' }}>
          <Segmented<RealityRegionCategory>
            value={region}
            onChange={handleRegionChange}
            options={[
              { label: t('pages.inbounds.form.realityCategoryAll'), value: 'all' },
              { label: t('pages.inbounds.form.realityCategoryUSWest'), value: 'us-west' },
              { label: t('pages.inbounds.form.realityCategoryTokyo'), value: 'tokyo' },
            ]}
          />
          <Input.Search
            style={{ flex: 1, minWidth: 260 }}
            aria-label={t('pages.inbounds.form.scanDiscoverPlaceholder')}
            allowClear
            enterButton={t('pages.inbounds.form.scan')}
            loading={loading}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onSearch={() => runScan(query.trim() || getTargetsCSVByRegion(region))}
            placeholder={t('pages.inbounds.form.scanDiscoverPlaceholder')}
          />
        </div>
        <Table<RealityScanResult>
          size="small"
          rowKey="target"
          loading={loading}
          columns={columns}
          dataSource={results}
          pagination={false}
          scroll={{ y: 360 }}
        />
      </Space>
    </Modal>
  );
}
