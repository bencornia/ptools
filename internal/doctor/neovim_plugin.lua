-- P, with plsp from ptools (https://github.com/bencornia/ptools), as plsp
-- doctor set it up. Neovim loads this from your configuration's plugin
-- directory. To change it, move it into your own configuration; plsp
-- doctor only replaces it while it starts with the line above.

-- Neovim detects .p files as Pascal or Progress, so claim the extension.
vim.filetype.add({ extension = { p = 'p' } })

vim.lsp.config('plsp', {
  cmd = { 'plsp' },
  filetypes = { 'p' },
  root_markers = { '.git' },
})
vim.lsp.enable('plsp')
