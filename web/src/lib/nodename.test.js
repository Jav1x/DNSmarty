import { describe, it, expect } from 'vitest'
import { nextNodeName } from './nodename'

describe('nextNodeName', () => {
  it('first node', () => expect(nextNodeName([])).toBe('node-1'))
  it('follows the highest existing node-N', () => expect(nextNodeName(['node-1', 'node-2'])).toBe('node-3'))
  // node.name UNIQUE: после удаления node-2 счётчик «число + 1» дал бы занятое имя.
  it('skips freed numbers of deleted nodes', () => expect(nextNodeName(['node-1', 'node-3'])).toBe('node-4'))
  it('ignores custom names', () => expect(nextNodeName(['gw-berlin'])).toBe('node-1'))
  it('multi-digit numbers', () => expect(nextNodeName(['node-9', 'node-10'])).toBe('node-11'))
})
