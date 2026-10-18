import React, { useEffect, useRef, useState, useCallback, useMemo } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useAuth } from './auth';

const NUM_ROWS = 50;
const NUM_COLS = 20;

export interface CellValue {
  kind: string;
  display: string;
  number?: number;
  error?: string;
  formula?: string;
}

export function colToLetter(col: number): string {
  let letter = '';
  let c = col;
  while (c >= 0) {
    letter = String.fromCharCode((c % 26) + 65) + letter;
    c = Math.floor(c / 26) - 1;
  }
  return letter;
}

export function letterToCol(letters: string): number {
  let col = 0;
  for (let i = 0; i < letters.length; i++) {
    col = col * 26 + (letters.charCodeAt(i) - 64);
  }
  return col - 1;
}

interface CellProps {
  row: number;
  col: number;
  cell?: CellValue;
  isSelected: boolean;
  isEditing: boolean;
  onSelect: (row: number, col: number) => void;
  onStartEdit: (row: number, col: number) => void;
  onCommit: (row: number, col: number, value: string) => void;
}

const GridCell = React.memo<CellProps>(
  ({ row, col, cell, isSelected, isEditing, onSelect, onStartEdit, onCommit }) => {
    const [editValue, setEditValue] = useState(cell?.formula || cell?.display || '');
    const colName = colToLetter(col);
    const cellRefName = `${colName}${row + 1}`;

    useEffect(() => {
      setEditValue(cell?.formula || cell?.display || '');
    }, [cell?.formula, cell?.display]);

    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'Enter') {
        onCommit(row, col, editValue);
      } else if (e.key === 'Escape') {
        setEditValue(cell?.formula || cell?.display || '');
        onSelect(row, col);
      }
    };

    return (
      <div
        data-testid={`cell-${cellRefName}`}
        data-cell={cellRefName}
        data-row={row}
        data-col={col}
        onClick={() => onStartEdit(row, col)}
        className={`h-7 border-b border-r border-gray-200 px-2 flex items-center text-xs truncate select-none cursor-cell font-sans ${
          isSelected ? 'ring-2 ring-blue-600 z-10 bg-blue-50' : 'bg-white hover:bg-gray-50'
        }`}
      >
        {isEditing ? (
          <input
            data-testid="cell-editor"
            type="text"
            autoFocus
            value={editValue}
            onChange={(e) => setEditValue(e.target.value)}
            onKeyDown={handleKeyDown}
            onBlur={() => onCommit(row, col, editValue)}
            className="w-full h-full p-0 border-none outline-none bg-transparent font-sans text-xs"
          />
        ) : (
          <span className="truncate w-full text-gray-900">
            {cell?.display ?? ''}
          </span>
        )}
      </div>
    );
  },
  (prev, next) => {
    return (
      prev.cell === next.cell &&
      prev.isSelected === next.isSelected &&
      prev.isEditing === next.isEditing
    );
  }
);

export const Grid: React.FC = () => {
  const { id: docId } = useParams<{ id: string }>();
  const { token } = useAuth();
  const navigate = useNavigate();

  const [cells, setCells] = useState<Record<string, CellValue>>({});
  const [selectedCell, setSelectedCell] = useState<{ row: number; col: number }>({ row: 0, col: 0 });
  const [isEditing, setIsEditing] = useState(false);
  const [formulaBarValue, setFormulaBarValue] = useState('');
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected'>('connecting');

  const wsRef = useRef<WebSocket | null>(null);
  const actorIdRef = useRef<string>('');
  const lastSeqRef = useRef<number>(0);

  // Performance timer
  console.time('grid-render');
  useEffect(() => {
    console.timeEnd('grid-render');
  });

  const selectedKey = `${selectedCell.row}:${selectedCell.col}`;
  const currentCell = cells[selectedKey];
  const selectedCellName = `${colToLetter(selectedCell.col)}${selectedCell.row + 1}`;

  useEffect(() => {
    setFormulaBarValue(currentCell?.formula || currentCell?.display || '');
  }, [selectedCell, currentCell]);

  const connectWs = useCallback(() => {
    if (!docId || !token) return;

    setStatus('connecting');
    let host = 'localhost:8080';
    if (typeof window !== 'undefined') {
      const customPort = localStorage.getItem('vss_server_port');
      if (customPort && customPort !== 'undefined') {
        host = `localhost:${customPort}`;
      }
    }

    const wsUrl = `ws://${host}/ws?token=${encodeURIComponent(token)}&doc_id=${encodeURIComponent(docId)}`;
    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      setStatus('connected');
      // On reconnect, send sync_request with lastSeq
      if (lastSeqRef.current > 0) {
        ws.send(
          JSON.stringify({
            type: 'sync_request',
            doc_id: docId,
            last_seq: lastSeqRef.current,
          })
        );
      }
    };

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        switch (msg.type) {
          case 'welcome': {
            actorIdRef.current = msg.actor_id || msg.user_id || '';
            // Request existing ops / state
            ws.send(
              JSON.stringify({
                type: 'sync_request',
                doc_id: docId,
                last_seq: lastSeqRef.current,
              })
            );
            break;
          }
          case 'sync_response': {
            if (msg.last_seq) {
              lastSeqRef.current = msg.last_seq;
            }
            if (msg.ops && Array.isArray(msg.ops)) {
              setCells((prev) => {
                const next = { ...prev };
                for (const op of msg.ops) {
                  if (op.type === 'grid.set_cell' && op.payload) {
                    const p = typeof op.payload === 'string' ? JSON.parse(op.payload) : op.payload;
                    const key = `${p.row}:${p.col}`;
                    next[key] = {
                      kind: p.formula ? 'formula' : 'text',
                      display: p.raw || '',
                      formula: p.formula || undefined,
                    };
                  }
                }
                return next;
              });
            }
            break;
          }
          case 'values': {
            if (msg.seq) {
              lastSeqRef.current = Math.max(lastSeqRef.current, msg.seq);
            }
            // Handler for MsgValues: updates ONLY the cells in the frame
            if (msg.values && Array.isArray(msg.values)) {
              setCells((prev) => {
                let changed = false;
                const next = { ...prev };
                for (const item of msg.values) {
                  const key = `${item.row}:${item.col}`;
                  if (JSON.stringify(prev[key]) !== JSON.stringify(item.value)) {
                    next[key] = item.value;
                    changed = true;
                  }
                }
                return changed ? next : prev;
              });
            }
            break;
          }
          case 'error': {
            console.error('WebSocket server error:', msg.error);
            break;
          }
        }
      } catch (e) {
        console.error('Error handling WebSocket message:', e);
      }
    };

    ws.onclose = () => {
      setStatus('disconnected');
    };

    ws.onerror = (err) => {
      console.error('WebSocket error:', err);
      setStatus('disconnected');
    };
  }, [docId, token]);

  useEffect(() => {
    connectWs();
    return () => {
      if (wsRef.current) {
        wsRef.current.close();
      }
    };
  }, [connectWs]);

  const commitCell = useCallback(
    (row: number, col: number, rawInput: string) => {
      setIsEditing(false);
      const isFormula = rawInput.trim().startsWith('=');
      const formula = isFormula ? rawInput.trim() : null;
      const raw = rawInput;

      // Optimistic update
      const key = `${row}:${col}`;
      setCells((prev) => ({
        ...prev,
        [key]: {
          kind: isFormula ? 'formula' : 'text',
          display: raw,
          formula: formula || undefined,
        },
      }));

      // Send op over WebSocket
      if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN && docId) {
        const opId =
          typeof crypto !== 'undefined' && crypto.randomUUID
            ? crypto.randomUUID()
            : `op-${Date.now()}-${Math.random()}`;

        const op = {
          id: opId,
          doc_id: docId,
          actor_id: actorIdRef.current || 'local',
          seq: 0,
          type: 'grid.set_cell',
          payload: {
            row,
            col,
            raw,
            formula,
          },
          timestamp: Date.now(),
        };

        const msg = {
          type: 'op',
          doc_id: docId,
          op,
        };

        wsRef.current.send(JSON.stringify(msg));
      }
    },
    [docId]
  );

  const handleFormulaBarKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      commitCell(selectedCell.row, selectedCell.col, formulaBarValue);
    }
  };

  const columns = useMemo(() => Array.from({ length: NUM_COLS }, (_, i) => i), []);
  const rows = useMemo(() => Array.from({ length: NUM_ROWS }, (_, i) => i), []);

  return (
    <div className="flex flex-col h-screen w-screen overflow-hidden bg-white font-sans text-gray-800">
      {/* Top Header */}
      <header className="h-12 border-b border-gray-200 px-4 flex items-center justify-between bg-white flex-shrink-0">
        <div className="flex items-center space-x-4">
          <button
            onClick={() => navigate('/docs')}
            className="text-gray-500 hover:text-gray-700 text-sm font-medium flex items-center space-x-1"
          >
            <span>←</span>
            <span>Documents</span>
          </button>
          <span className="text-gray-300">|</span>
          <span className="font-semibold text-gray-900 text-sm">Spreadsheet</span>
        </div>

        <div className="flex items-center space-x-3">
          <span
            data-testid="connection-status"
            className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
              status === 'connected'
                ? 'bg-green-100 text-green-800'
                : status === 'connecting'
                ? 'bg-yellow-100 text-yellow-800'
                : 'bg-red-100 text-red-800'
            }`}
          >
            ● {status}
          </span>
          {status === 'disconnected' && (
            <button
              data-testid="reconnect-btn"
              onClick={connectWs}
              className="text-xs px-2 py-1 bg-gray-100 hover:bg-gray-200 text-gray-700 rounded border border-gray-300 font-medium"
            >
              Reconnect
            </button>
          )}
        </div>
      </header>

      {/* Formula Bar */}
      <div className="h-9 border-b border-gray-200 px-3 flex items-center space-x-2 bg-gray-50 flex-shrink-0">
        <div
          data-testid="selected-cell-name"
          className="w-16 text-center text-xs font-bold text-gray-600 bg-white border border-gray-300 rounded py-1"
        >
          {selectedCellName}
        </div>
        <span className="text-gray-400 font-serif italic text-sm select-none">fx</span>
        <input
          data-testid="formula-bar-input"
          type="text"
          value={formulaBarValue}
          onChange={(e) => setFormulaBarValue(e.target.value)}
          onKeyDown={handleFormulaBarKeyDown}
          className="flex-1 px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-blue-500 bg-white"
          placeholder="Enter a value or formula (=A1+B1)"
        />
      </div>

      {/* Grid Canvas */}
      <div className="flex-1 overflow-auto bg-gray-100 select-none">
        <div className="inline-block min-w-full">
          {/* Header Row (Columns A..T) */}
          <div className="flex sticky top-0 z-20 bg-gray-100 border-b border-gray-300">
            <div className="w-12 h-6 border-r border-gray-300 bg-gray-200 flex-shrink-0" />
            {columns.map((col) => (
              <div
                key={col}
                className="w-24 h-6 border-r border-gray-300 flex items-center justify-center text-xs font-semibold text-gray-600 bg-gray-100 flex-shrink-0 select-none"
              >
                {colToLetter(col)}
              </div>
            ))}
          </div>

          {/* Grid Rows (1..50) */}
          {rows.map((row) => (
            <div key={row} className="flex">
              {/* Row Header */}
              <div className="w-12 h-7 border-b border-r border-gray-300 flex items-center justify-center text-xs font-semibold text-gray-500 bg-gray-100 sticky left-0 z-10 flex-shrink-0 select-none">
                {row + 1}
              </div>
              {/* Row Cells */}
              {columns.map((col) => {
                const k = `${row}:${col}`;
                const isSelected = selectedCell.row === row && selectedCell.col === col;
                const cellEditing = isSelected && isEditing;
                return (
                  <div key={col} className="w-24 flex-shrink-0">
                    <GridCell
                      row={row}
                      col={col}
                      cell={cells[k]}
                      isSelected={isSelected}
                      isEditing={cellEditing}
                      onSelect={(r, c) => {
                        setSelectedCell({ row: r, col: c });
                        setIsEditing(false);
                      }}
                      onStartEdit={(r, c) => {
                        setSelectedCell({ row: r, col: c });
                        setIsEditing(true);
                      }}
                      onCommit={commitCell}
                    />
                  </div>
                );
              })}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
};
