"""Split large Go files into smaller modules."""
import os, re

def find_func(lines, name):
    """Find start and end line of a function."""
    for i, line in enumerate(lines):
        if line.strip().startswith('func ' + name + '('):
            depth = 0
            for j in range(i, len(lines)):
                depth += lines[j].count('{') - lines[j].count('}')
                if depth == 0 and j > i:
                    return i, j + 1
    return -1, -1

def extract_block(lines, start_pattern, end_pattern=None):
    """Extract lines between patterns."""
    result = []
    in_block = False
    for i, line in enumerate(lines):
        if start_pattern in line:
            in_block = True
        if in_block:
            result.append(i)
        if in_block and end_pattern and end_pattern in line:
            break
        if in_block and not end_pattern and line.strip() == '}' and len(result) > 2:
            break
    return result

def write_go_file(path, pkg, imports, lines, idxs):
    """Write extracted lines to a new Go file."""
    with open(path, 'w', encoding='utf-8') as f:
        f.write(f'package {pkg}\n\n')
        f.write('import (\n')
        for imp in imports:
            f.write(f'\t"{imp}"\n')
        f.write(')\n\n')
        for i in sorted(idxs):
            f.write(lines[i])
    return len(idxs)

# ─── Read source files ───
with open('internal/agent/agent.go', 'r', encoding='utf-8') as f:
    agent = f.readlines()

# ─── Extract sections by line patterns ───
extracted = set()

# worklog.go: workEntry, workLogBlob, workSummary, reflectPrompt, MaxToolCalls, TaskTimeout, compactKeep, estMsgSize, estimateCtxSize, estimateTokens
wl_idxs = set()
for i, line in enumerate(agent):
    if any(p in line for p in [
        'const MaxToolCalls', 'type workEntry struct', 'const TaskTimeout',
        'const compactKeep', 'const reflectPrompt',
    ]):
        wl_idxs.add(i)
        if 'const reflectPrompt' in line:
            for j in range(i, min(i+10, len(agent))):
                wl_idxs.add(j)
                if agent[j].strip().endswith('工具调用。"'):
                    break
        if 'type workEntry struct' in line:
            for j in range(i, min(i+20, len(agent))):
                wl_idxs.add(j)
                if agent[j].strip() == '}':
                    break

for name in ['workLogBlob', 'workSummary', 'estMsgSize', 'estimateCtxSize', 'estimateTokens']:
    s, e = find_func(agent, name)
    if s >= 0:
        for j in range(s, e):
            wl_idxs.add(j)

write_go_file('internal/agent/worklog.go', 'agent',
    ['fmt', 'strings'], agent, wl_idxs)
extracted |= wl_idxs
print(f'worklog.go: {len(wl_idxs)} lines')

# dedup.go
dd_idxs = set()
for name in ['dedupReadFile', 'dedupListDir', 'dedupGrep', 'parseReadArgs']:
    s, e = find_func(agent, name)
    if s >= 0:
        for j in range(s, e):
            dd_idxs.add(j)

write_go_file('internal/agent/dedup.go', 'agent',
    ['encoding/json', 'fmt', 'strings'], agent, dd_idxs)
extracted |= dd_idxs
print(f'dedup.go: {len(dd_idxs)} lines')

# render.go
rd_idxs = set()
for name in ['formatToolLabel', 'toolIcon', 'tokenBadge', 'truncateRunes', 'compactArgs', 'statusLine', 'isError', 'indentLines']:
    s, e = find_func(agent, name)
    if s >= 0:
        for j in range(s, e):
            rd_idxs.add(j)

write_go_file('internal/agent/render.go', 'agent',
    ['encoding/json', 'fmt', 'strings'], agent, rd_idxs)
extracted |= rd_idxs
print(f'render.go: {len(rd_idxs)} lines')

# Remove extracted lines from agent.go
new_agent = [agent[i] for i in range(len(agent)) if i not in extracted]

# Clean up unused imports
agent_text = ''.join(new_agent)
# Remove "context" import if present
agent_text = agent_text.replace('\t"gowhale/internal/context"\n', '')
# Remove empty import groups
agent_text = re.sub(r'import \(\s*\)', '', agent_text)
# Clean double+ newlines
while '\n\n\n' in agent_text:
    agent_text = agent_text.replace('\n\n\n', '\n\n')

with open('internal/agent/agent.go', 'w', encoding='utf-8') as f:
    f.write(agent_text)
print(f'agent.go after: {len(new_agent) - len(extracted)} lines kept')

# ─── Now split chatroom.go ───
with open('internal/agent/chatroom.go', 'r', encoding='utf-8') as f:
    chat = f.readlines()

ch_extracted = set()

# chat_prompts.go: all const prompts
prompt_idxs = set()
in_prompt = False
for i, line in enumerate(chat):
    if line.strip().startswith('const ') and ('Prompt' in line or 'SystemPrompt' in line):
        in_prompt = True
        prompt_idxs.add(i)
    elif in_prompt:
        prompt_idxs.add(i)
        if line.strip() == '`' or (line.strip().startswith('`') and not line.strip().startswith('const')):
            if not line.strip().startswith('const'):
                in_prompt = False

write_go_file('internal/agent/chat_prompts.go', 'agent', [], chat, prompt_idxs)
ch_extracted |= prompt_idxs
print(f'chat_prompts.go: {len(prompt_idxs)} lines')

# chat_helpers.go: truncateResult, toolCallNames, hasNewReadTarget, isReadOnlyTool, isProductiveTool, executeDevTool, filterTools, buildContextBlock, addToHistory, ClassifyChatRoom, classifyByLLM
help_idxs = set()
for name in ['truncateResult', 'toolCallNames', 'hasNewReadTarget', 'executeDevTool',
             'isReadOnlyTool', 'isProductiveTool', 'filterTools', 'buildContextBlock',
             'addToHistory', 'ClassifyChatRoom', 'classifyByLLM']:
    s, e = find_func(chat, name)
    if s >= 0:
        for j in range(s, e):
            help_idxs.add(j)

write_go_file('internal/agent/chat_helpers.go', 'agent',
    ['encoding/json', 'fmt', 'strings', 'gowhale/internal/llm', 'gowhale/internal/tools'],
    chat, help_idxs)
ch_extracted |= help_idxs
print(f'chat_helpers.go: {len(help_idxs)} lines')

# chat_roles.go: runPMTurn, runDevTurn, runQATurn, runUserProxyTurn, runRoleWithTools
role_idxs = set()
for name in ['runPMTurn', 'runDevTurn', 'runQATurn', 'runUserProxyTurn', 'runRoleWithTools']:
    s, e = find_func(chat, name)
    if s >= 0:
        for j in range(s, e):
            role_idxs.add(j)

write_go_file('internal/agent/chat_roles.go', 'agent',
    ['encoding/json', 'fmt', 'strings', 'gowhale/internal/llm'],
    chat, role_idxs)
ch_extracted |= role_idxs
print(f'chat_roles.go: {len(role_idxs)} lines')

# chat_loop.go: runLoop, finishWithSummary, generateFinalSummary
loop_idxs = set()
for name in ['runLoop', 'finishWithSummary', 'generateFinalSummary']:
    s, e = find_func(chat, name)
    if s >= 0:
        for j in range(s, e):
            loop_idxs.add(j)

write_go_file('internal/agent/chat_loop.go', 'agent',
    ['fmt', 'time', 'gowhale/internal/llm'],
    chat, loop_idxs)
ch_extracted |= loop_idxs
print(f'chat_loop.go: {len(loop_idxs)} lines')

# Remove from chatroom.go
new_chat = [chat[i] for i in range(len(chat)) if i not in ch_extracted]
chat_text = ''.join(new_chat)
while '\n\n\n' in chat_text:
    chat_text = chat_text.replace('\n\n\n', '\n\n')

with open('internal/agent/chatroom.go', 'w', encoding='utf-8') as f:
    f.write(chat_text)
print(f'chatroom.go after: {len(new_chat)} lines kept')
