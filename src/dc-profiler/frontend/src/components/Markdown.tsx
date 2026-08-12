import '@mdxeditor/editor/style.css';
import 'github-markdown-css/github-markdown-light.css';
import Box from '@mui/material/Box';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import {
  MDXEditor,
  headingsPlugin,
  listsPlugin,
  quotePlugin,
  thematicBreakPlugin,
  linkPlugin,
  linkDialogPlugin,
  tablePlugin,
  markdownShortcutPlugin,
  codeBlockPlugin,
  codeMirrorPlugin,
  diffSourcePlugin,
  toolbarPlugin,
  UndoRedo,
  BoldItalicUnderlineToggles,
  BlockTypeSelect,
  ListsToggle,
  CreateLink,
  InsertTable,
  InsertCodeBlock,
  Separator,
  type MDXEditorMethods,
} from '@mdxeditor/editor';

// Code-block languages MDXEditor should recognize. Gemini emits ```sql for
// sample queries; the empty key is the fallback for untagged fences. These
// render as plain (non-highlighted) code blocks, matching the Workbench editor.
const codeBlockLanguages = {
  '': 'Plain text',
  sql: 'SQL',
  text: 'Text',
  json: 'JSON',
  bash: 'Shell',
};

// sxMarkdown tightens github-markdown-css defaults so rendered content reads as
// a compact box rather than a full document.
const sxMarkdown = {
  '&.markdown-body': {
    fontSize: 14,
    bgcolor: 'transparent',
  },
  '& .markdown-body': {
    fontSize: 14,
    bgcolor: 'transparent',
  },
  '& p:first-of-type': {mt: 0},
  '& p:last-child': {mb: 0},
} as const;

/**
 * MarkdownView renders read-only markdown the way it will appear in the catalog.
 * It uses the same `.markdown-body` (github-markdown-css) styling as the editor
 * so the Current / AI boxes look consistent with the WYSIWYG "Your value".
 */
export function MarkdownView({markdown}: {markdown: string}) {
  return (
    <Box className="markdown-body" sx={sxMarkdown}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{markdown}</ReactMarkdown>
    </Box>
  );
}

interface MarkdownEditorProps {
  markdown: string;
  onChange: (md: string) => void;
  editorRef?: React.Ref<MDXEditorMethods>;
}

/**
 * MarkdownEditor is a WYSIWYG markdown editor mirroring the one used on the
 * Workbench data-collection page (@mdxeditor/editor): the edited content is
 * shown rendered, so it doubles as its own preview.
 *
 * MDXEditor only reads `markdown` on mount, so callers that need to swap the
 * content programmatically (e.g. the Original / AI toggle buttons) should bump
 * a React `key` to force a remount.
 */
export function MarkdownEditor({markdown, onChange, editorRef}: MarkdownEditorProps) {
  return (
    <Box
      sx={{
        border: '1px solid',
        borderColor: 'divider',
        borderRadius: 1,
        '& .mdxeditor': {borderRadius: 1},
        '& .mdxeditor-toolbar': {borderRadius: '4px 4px 0 0'},
        '& [class*="_contentEditable"]': {minHeight: 120},
      }}
    >
      <MDXEditor
        ref={editorRef}
        markdown={markdown}
        onChange={onChange}
        onError={() => {
          /* Malformed markdown falls back to source view; nothing to do. */
        }}
        contentEditableClassName="markdown-body"
        plugins={[
          headingsPlugin(),
          listsPlugin(),
          quotePlugin(),
          thematicBreakPlugin(),
          linkPlugin(),
          linkDialogPlugin(),
          tablePlugin(),
          markdownShortcutPlugin(),
          codeBlockPlugin({defaultCodeBlockLanguage: ''}),
          codeMirrorPlugin({codeBlockLanguages}),
          diffSourcePlugin({viewMode: 'rich-text'}),
          toolbarPlugin({
            toolbarContents: () => (
              <>
                <UndoRedo />
                <Separator />
                <BoldItalicUnderlineToggles />
                <Separator />
                <BlockTypeSelect />
                <ListsToggle />
                <Separator />
                <CreateLink />
                <InsertTable />
                <InsertCodeBlock />
              </>
            ),
          }),
        ]}
      />
    </Box>
  );
}
