# Verificação de prestadores e destinatários — 11 de setembro de 2026

Estado: **levantamento factual incompleto e não aprovativo**. Este registo separa factos observados, termos públicos dos fornecedores e elementos que só o responsável pelo tratamento pode confirmar. Não autoriza a ativação do executor de privacidade.

Correção do responsável em `2026-09-11`: o servidor MyCFCoimbra não envia nem copia perfis ou outros dados para a FPC. A plataforma apenas guarda um número de licença introduzido por um administrador e constrói ligações externas; ao abri-las, o navegador contacta a FPC através de um endereço que contém esse número. Esta confirmação substitui, para o inventário técnico MyCFCoimbra, a inferência anterior de que a atividade federativa externa do CFC constituía um fluxo da aplicação.

Observação de produção: `2026-09-11T09:21:54Z`, versão Git `72d095c1e22de80f6d07a0cf2a890afb9924ded7` (`v1.19.0`), imagem `sha256:ad66674270d8148706f63a701b0a4d902b7bdfb3547ed2d174864aa4c466670e`. Referências duráveis: [#109](https://github.com/ricardoespsanto/mycfc/issues/109#issuecomment-5632337471), [#246](https://github.com/ricardoespsanto/mycfc/issues/246#issuecomment-5632337666), [#247](https://github.com/ricardoespsanto/mycfc/issues/247#issuecomment-5632337842) e [#248](https://github.com/ricardoespsanto/mycfc/issues/248#issuecomment-5632338048). Os resultados detalhados e identidades exatas pertencem à evidência operacional restrita; este documento conserva apenas resultados agregados e referências.

## Método e regra de prova

Foram comparados o código e a configuração declarativa da versão `v1.19.0`, observações agregadas de produção e fontes oficiais públicas. Uma página pública prova apenas o conteúdo publicado pelo fornecedor; não prova adesão contratual, opção regional, funcionalidade ativa, retenção configurada ou tratamento efetivo na conta do CFC. Esses elementos exigem captura autenticada da conta, contrato/ata ou evidência operacional datada.

## Factos observados no MyCFC

- A produção usa AWS `eu-west-1` e declara S3, KMS, SES e CloudWatch; Route 53 e ECR suportam a operação sem receber dados de membros por desenho.
- A identidade AWS ordinária do servidor está impedida de enumerar versões dos três prefixos privados e de ler a postura global de CloudWatch/SES. Este bloqueio é um controlo de privilégio mínimo, não uma lacuna a remover. A identidade exata pertence à evidência operacional restrita, não a este documento público.
- A aplicação é executada num servidor Hetzner e usa um túnel/reverse proxy Cloudflare. O proxy pode processar transitoriamente cabeçalhos, cookies, corpos de pedido e conteúdo de resposta, incluindo categorias especiais quando essas rotas são usadas; isto é distinto de logging ou conservação, que dependem da configuração real. O código limita a localização Hetzner prevista a `fsn1` ou `nbg1`, mas a localização efetiva da conta não foi autenticada nesta verificação.
- O GitHub processa código, CI, análises e artefactos de entrega. Dados pessoais de produção são proibidos nesse fluxo; a retenção declarada pelo CFC é 14 dias para artefactos CI e 30 dias para ensaios/deployment, ainda por comparar com as definições da organização.
- À hora da observação não existiam ligações por titular a wearables/prestadores na produção. O responsável confirmou também que o servidor MyCFCoimbra não transmite dados à FPC; a navegação iniciada pelo utilizador para um endereço FPC que contém o número de licença não é uma integração servidor-a-servidor. A lista de integrações externas por titular é, portanto, completa e vazia.

## Fontes oficiais verificadas

### Hetzner

- [Data Protection at Hetzner](https://docs.hetzner.com/general/company-and-policy/data-protection-at-hetzner/) — o DPA regula o tratamento por conta do cliente, é concluído na conta, exige indicar categorias de dados e titulares e disponibiliza listas de subcontratantes e TOMs.
- [DPA de exemplo](https://www.hetzner.com/AV/DPA_en.pdf) — modelo do contrato do artigo 28.º; não prova que a conta do CFC o tenha concluído.
- [Medidas técnicas e organizativas](https://docs.hetzner.com/general/security-and-identify/technical-and-organizational-measures/) — descreve medidas e confirma a responsabilidade do cliente pela administração e segurança do servidor cloud.

Pendente: exportar da conta o DPA vigente e anexos, confirmar a localização do servidor, inventariar backups/snapshots e registar a revisão dos subcontratantes e TOMs.

### Amazon Web Services

- [Centro RGPD AWS](https://aws.amazon.com/compliance/gdpr-center/) e [FAQ de conformidade](https://aws.amazon.com/compliance/faq/) — o DPA RGPD integra os termos AWS e aplica-se automaticamente às atividades abrangidas; as SCC aplicam-se quando necessárias.
- [Subprocessadores AWS](https://aws.amazon.com/compliance/sub-processors/) — os subprocessadores aplicáveis dependem das regiões e serviços escolhidos.
- [FAQ de privacidade](https://aws.amazon.com/compliance/data-privacy-faq/) — o cliente escolhe a região do conteúdo e a AWS declara que não o move ou replica fora dela sem acordo, ressalvadas as características do serviço.

Pendente: inventário autenticado dos serviços e opções ativas, retenções CloudWatch/S3/SES, supressões e destinos SES, serviços globais, acessos de suporte e subconjunto de subprocessadores aplicável.

### Cloudflare

- [Centro RGPD Cloudflare](https://www.cloudflare.com/trust-hub/gdpr/) — informação pública de conformidade e transferências.
- [DPA Cloudflare v6.4](https://cf-assets.www.cloudflare.com/slt3lc6tev37/1TTgT35GoUNlKZYGuKWBFy/4e7dfc8cf402419a9b1cf624291fc69f/cloudflare_customer_dpa-v6.4_april_3_2026.pdf) — termos públicos de tratamento e subprocessamento; não prova o âmbito contratual da conta CFC.

Pendente: confirmar plano, DPA/termos aceites, produtos e funcionalidades ativos, opções de localização, retenção de logs/sinais, subprocessadores aplicáveis e canal de eliminação.

### GitHub

- [Informação de privacidade GitHub](https://docs.github.com/en/site-policy/privacy-policies/github-general-privacy-statement) e [subprocessadores](https://docs.github.com/en/site-policy/privacy-policies/github-subprocessors) — a GitHub descreve a existência de um DPA e limita a lista publicada aos serviços por ele abrangidos; a aplicabilidade concreta depende da relação contratual. A página pública do DPA deve ser exportada no pacote controlado quando estiver acessível.
- [Retenção de artefactos e logs de Actions](https://docs.github.com/en/organizations/managing-organization-settings/configuring-the-retention-period-for-github-actions-artifacts-and-logs-in-your-organization) — a organização pode configurar o período dentro dos limites do plano.

Pendente: confirmar o tipo de conta/organização, o DPA aplicável, a retenção configurada, acessos e a ausência de dados pessoais de produção em artefactos, logs, issues e suporte.

### Federação Portuguesa de Canoagem — serviço externo, não integração MyCFCoimbra

- [Portal oficial de filiações 2026](https://inscricoes.fpcanoagem.pt/login.php) — declara a filiação online obrigatória, identifica a FPC como responsável pelo tratamento, descreve o histórico consultável pelo clube e pede dados/documentos de agentes, incluindo identificação, contactos, fotografia, comprovativo de identificação e exame médico quando aplicável.

Este material descreve o serviço próprio da FPC, não uma divulgação observada pelo MyCFCoimbra. Qualquer operação federativa que o CFC realize fora da plataforma pertence ao respetivo processo externo e não ao inventário técnico ou ao executor MyCFCoimbra.

Documento referenciado mas não verificado: [Política FPC RGPD 2025](https://www.fpcanoagem.pt/uploads/docs/nacional/FPC-RGPD2025.pdf). O portal oficial liga esta cópia, mas o servidor recusou leitura automatizada nesta verificação. O responsável deve obter, guardar com data e hash e rever a cópia antes de a usar como prova.

## Consequência para a ativação

A confirmação do responsável permite uma lista vazia apenas para o inventário fechado de integrações externas por titular do MyCFCoimbra. Essa lista continua a exigir prova assinada de que o inventário é completo; ausência de prova, lista parcial, integração inesperada ou serviço futuro permanecem `NOT_READY` e bloqueiam a ativação.

À hora da observação, o registo técnico estava fechado, a kill switch estava ativa e os timers de privacidade estavam desligados. Até existir a prova de conta/fluxo, estes controlos devem permanecer nesse estado e nenhum apagamento real pode ser concluído como se os destinatários externos estivessem tratados.

## Pacote de aprovação ainda necessário

1. Ata datada da Direção que aprove a matriz, os fundamentos/exceções e os responsáveis operacionais.
2. Autoridade de casos de privacidade distinta do acesso administrativo e política de representação de menores.
3. Evidência autenticada de Hetzner, AWS, Cloudflare e GitHub, com revisão e validade definidas.
4. Quando existirem fluxos MyCFCoimbra para seguradora, organizador ou autoridade concreta, mapas equivalentes por divulgação; operações externas do CFC não são inferidas como fluxos da aplicação.
5. Revisão jurídica dos fundamentos, incluindo condições e salvaguardas do artigo 9.º e prazos externos.
