-- Run by plsp doctor on a P file, with the user's configuration: writes
-- Neovim's configuration directory, the file's filetype, and the language
-- servers that attach to it, to $PLSP_DOCTOR_OUT.
local out = {
  'config=' .. vim.fn.stdpath('config'),
  'filetype=' .. vim.bo.filetype,
}
if vim.bo.filetype == 'p' then
  vim.wait(5000, function()
    return #vim.lsp.get_clients({ bufnr = 0 }) > 0
  end, 50)
end
for _, client in ipairs(vim.lsp.get_clients({ bufnr = 0 })) do
  local cmd = client.config.cmd
  if type(cmd) == 'table' then
    cmd = cmd[1]
  else
    cmd = tostring(cmd)
  end
  table.insert(out, 'client=' .. client.name .. '\t' .. cmd)
end
vim.fn.writefile(out, os.getenv('PLSP_DOCTOR_OUT'))
vim.cmd('qall!')
