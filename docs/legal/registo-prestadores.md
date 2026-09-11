# Registo de prestadores e destinatários externos

Estado: modelo de governação aprovado em `2026-09-08`; verificação pública inicial registada em `2026-09-11`; prova contratual e configuração real de conta continuam pendentes. A evidência e os limites da verificação estão em `evidencia-prestadores-2026-09-11.md`. Este documento não transforma termos públicos em prova de adesão, nem confirma configurações de conta, subprocessadores aplicáveis ou transferências efetivas.

## Regra de manutenção

Antes da ativação e depois anualmente, bem como antes de qualquer novo prestador, subprocessador, região ou serviço material, registar e aprovar: papel jurídico, finalidade, categorias, titulares, serviço/produto, região de dados e acesso, contrato/DPA, subprocessadores, mecanismo e avaliação de transferência, retenção configurada/contratual, canal de eliminação, prova verificada, responsável e data da próxima revisão. Preferir tratamento no EEE; qualquer acesso fora do EEE exige mecanismo e avaliação documentados.

## Inventário técnico conhecido

| Entidade | Papel indicado / a confirmar por fluxo | Utilização conhecida no repositório/operação | Dados/categorias observados ou possíveis | Evidência pública verificada | Evidência de conta/fluxo ainda necessária |
|---|---|---|---|---|---|
| Hetzner Online GmbH | Subcontratante para conteúdo alojado se o DPA abranger o fluxo; responsável próprio para dados de conta/faturação | Servidor, aplicação, PostgreSQL e backups do servidor | Dados alojados, metadados técnicos e cópias | A documentação oficial confirma que o DPA não é automático, é concluído na conta, inclui categorias escolhidas pelo cliente e remete para subprocessadores e TOMs | DPA efetivamente concluído, anexos, papel por fluxo, localização real do servidor, relatório TOM, backups/snapshots, acessos, retenção e eliminação |
| Amazon Web Services | Subcontratante para conteúdo do cliente nos serviços abrangidos; responsável próprio para certos dados de conta/operação | `eu-west-1`; S3/KMS, backups, SES, CloudWatch e configuração operacional; Route 53/ECR sem dados de membro por desenho | Objetos e cópias cifradas, endereço/conteúdo de email, registos técnicos e metadados de conta/operação | O DPA RGPD integra os termos e aplica-se automaticamente quando abrangido; região e serviços determinam subprocessadores aplicáveis. O conteúdo permanece na região escolhida salvo acordo/exceções do serviço | Inventário de serviços da conta, papel por fluxo, configurações e retenções efetivas, opções globais/transferências, subprocessadores aplicáveis, supressões SES e rotas de eliminação |
| Cloudflare, Inc. | Subcontratante ou responsável conforme o produto e metadado; confirmar por fluxo | Tunnel/reverse proxy, DNS, proteção de rede e Turnstile | Em trânsito pelo proxy: IP, URL, cabeçalhos/cookies de sessão, corpo do pedido e conteúdo da resposta, podendo incluir perfil, contactos ou saúde na respetiva rota; sinais técnicos/de segurança e token de desafio. Distinguir trânsito de qualquer log/conservação | DPA público e informação RGPD/TOM disponíveis; a localização e retenção dependem do produto e configuração | DPA/termos da conta, TLS e produtos/funcionalidades efetivamente ativos, logging/conservação, localização/acessos, subprocessadores aplicáveis, transferências e eliminação |
| GitHub, Inc. | Subcontratante apenas quando a relação/serviço esteja abrangido pelo DPA | CI e artefactos de deployment | Código, identidade dos colaboradores e dados sintéticos; dados pessoais de produção são proibidos | Informação pública sobre DPA e subprocessadores existe; retenção de Actions é configurável | Tipo de conta/organização e DPA aplicável, definições reais de retenção e acesso, região quando contratada e confirmação de que não existem dados pessoais de produção |
| Organizadores, seguradora e autoridades | Normalmente responsáveis autónomos; confirmar por fluxo | Incidente, seguro, prova ou obrigação concreta | Apenas os campos estritamente necessários ao fluxo documentado; nenhum conjunto real foi confirmado | Nenhuma entidade concreta foi confirmada para um fluxo MyCFC | Identidade, fundamento, campos mínimos, prazo, canal de direitos/notificação, resposta e prova por divulgação |

SMTP de produção está desenhado para AWS SES. Qualquer substituição constitui alteração material e exige atualização prévia deste registo e da matriz.

## Ligações externas excluídas

O responsável confirmou em `2026-09-11` que o servidor MyCFCoimbra não envia nem copia perfis ou outros dados para a Federação Portuguesa de Canoagem. A plataforma guarda apenas o número de licença FPC introduzido por um administrador e utiliza-o para construir ligações externas para páginas de histórico da FPC, visíveis apenas a utilizadores já autorizados a consultar o perfil. Ao abrir uma ligação, o navegador contacta diretamente o website da FPC e o endereço de destino contém esse número de licença; não existe transmissão servidor-a-servidor, importação de resultados nem cópia do perfil MyCFCoimbra. Operações do CFC realizadas fora da plataforma, incluindo num portal federativo, não pertencem a este inventário e não criam uma obrigação de execução do artigo 19.º dentro de um apagamento MyCFCoimbra.

## Eliminação e prova

- Configurar a menor retenção suportada compatível com a matriz aprovada.
- #111 executa API, lifecycle, revogação ou pedido contratual quando disponível, com repetição limitada e logs opacos.
- Uma entidade já desligada ou um objeto já ausente conta como sucesso idempotente.
- Uma mensagem já entregue ou um registo sob controlo de responsável autónomo pode não ser eliminável pelo CFC; documentar o limite e informar quando aplicável, sem deixar o caso indefinidamente pendente.
- Guardar apenas referência, ação, resultado, data e prova operacional sem copiar o dado apagado.
